package activity

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func toolEvent(session domain.SessionID, at time.Time, kind domain.ActivityEventKind) domain.ActivityEvent {
	return domain.ActivityEvent{
		SessionID: session, Kind: kind, At: at, Tool: "Read", Target: "hooks.go",
		TTLMs:  domain.DurationMs(domain.DetailTTL(kind)),
		Coarse: domain.CoarseWorking, CoarseTTLMs: domain.DurationMs(domain.CoarseWorkingTTL),
	}
}

func recv(t *testing.T, ch <-chan domain.ActivityEvent) (domain.ActivityEvent, bool) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		return ev, ok
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for an activity event")
		return domain.ActivityEvent{}, false
	}
}

func TestHub_FansOutToEverySubscriber(t *testing.T) {
	h := NewHub()
	a, closeA := h.Subscribe("")
	defer closeA()
	b, closeB := h.Subscribe("")
	defer closeB()

	ev := toolEvent("ao-7", time.Now().UTC(), domain.ActivityEventToolStart)
	if err := h.Publish(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []<-chan domain.ActivityEvent{a, b} {
		got, _ := recv(t, ch)
		if got.SessionID != "ao-7" || got.Tool != "Read" {
			t.Errorf("got %+v, want the published event", got)
		}
	}
}

// The overlay wants every session at once, so an empty filter subscribes to all;
// a session-scoped subscriber must not see another session's events.
func TestHub_SessionFilter(t *testing.T) {
	h := NewHub()
	all, closeAll := h.Subscribe("")
	defer closeAll()
	one, closeOne := h.Subscribe("ao-7")
	defer closeOne()

	now := time.Now().UTC()
	if err := h.Publish(context.Background(), toolEvent("ao-9", now, domain.ActivityEventToolStart)); err != nil {
		t.Fatal(err)
	}
	if err := h.Publish(context.Background(), toolEvent("ao-7", now, domain.ActivityEventToolFailed)); err != nil {
		t.Fatal(err)
	}

	if got, _ := recv(t, all); got.SessionID != "ao-9" {
		t.Errorf("all-sessions subscriber got %q first, want ao-9", got.SessionID)
	}
	if got, _ := recv(t, all); got.SessionID != "ao-7" {
		t.Errorf("all-sessions subscriber got %q second, want ao-7", got.SessionID)
	}
	got, _ := recv(t, one)
	if got.SessionID != "ao-7" {
		t.Errorf("session-scoped subscriber got %q, want only ao-7", got.SessionID)
	}
	select {
	case extra := <-one:
		t.Errorf("session-scoped subscriber received a foreign event: %+v", extra)
	default:
	}
}

// The feed sits on the hot path of every tool call, so a slow consumer must be
// dropped, never allowed to block the agent's hook.
func TestHub_DropsRatherThanBlocks(t *testing.T) {
	h := NewHub()
	ch, unsubscribe := h.Subscribe("")
	defer unsubscribe()

	base := time.Now().UTC()
	for i := 0; i < subscriberBuffer*3; i++ {
		// Space the events past the throttle so none is dropped for coalescing.
		ev := toolEvent("ao-7", base.Add(time.Duration(i)*time.Second), domain.ActivityEventToolFailed)
		if err := h.Publish(context.Background(), ev); err != nil {
			t.Fatalf("publish %d must never fail on a full subscriber: %v", i, err)
		}
	}
	if len(ch) != subscriberBuffer {
		t.Errorf("buffered %d events, want the buffer capped at %d", len(ch), subscriberBuffer)
	}
}

func TestHub_UnsubscribeClosesTheChannel(t *testing.T) {
	h := NewHub()
	ch, unsubscribe := h.Subscribe("")
	unsubscribe()
	if _, ok := recv(t, ch); ok {
		t.Error("unsubscribe must close the channel")
	}
	unsubscribe() // idempotent
	if err := h.Publish(context.Background(), toolEvent("ao-7", time.Now().UTC(), domain.ActivityEventToolStart)); err != nil {
		t.Errorf("publishing with no subscribers must succeed: %v", err)
	}
}

func TestHub_NilHubIsSafe(t *testing.T) {
	var h *Hub
	ch, unsubscribe := h.Subscribe("")
	if _, ok := <-ch; ok {
		t.Error("a nil hub yields a closed channel")
	}
	unsubscribe()
	if err := h.Publish(context.Background(), domain.ActivityEvent{}); err != nil {
		t.Errorf("a nil hub must publish without error: %v", err)
	}
}

// A busy agent fires several tool hooks per second. Throttling caps an
// always-on-top overlay's redraw rate for every consumer at once. Dropping a
// tick is not a lie — the TTL still bounds whatever is on screen.
func TestHub_ThrottlesRapidToolTicks(t *testing.T) {
	h := NewHub()
	ch, unsubscribe := h.Subscribe("")
	defer unsubscribe()

	base := time.Now().UTC()
	must := func(ev domain.ActivityEvent) {
		t.Helper()
		if err := h.Publish(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}
	must(toolEvent("ao-7", base, domain.ActivityEventToolStart))
	must(toolEvent("ao-7", base.Add(50*time.Millisecond), domain.ActivityEventToolEnd))    // throttled
	must(toolEvent("ao-7", base.Add(100*time.Millisecond), domain.ActivityEventToolStart)) // throttled
	// A different session has its own budget.
	must(toolEvent("ao-9", base.Add(110*time.Millisecond), domain.ActivityEventToolStart))
	must(toolEvent("ao-7", base.Add(coalesceWindow), domain.ActivityEventToolEnd)) // window elapsed

	got := drain(ch)
	if len(got) != 3 {
		t.Fatalf("delivered %d events, want 3 (two throttled): %+v", len(got), got)
	}
	if got[0].At != base || got[1].SessionID != "ao-9" || got[2].At != base.Add(coalesceWindow) {
		t.Errorf("wrong events survived throttling: %+v", got)
	}
}

// Throttling must never swallow a failure or a level change: those are the
// events that keep the coarse rung — and therefore the decay — truthful.
func TestHub_NeverThrottlesFailuresOrLevelChanges(t *testing.T) {
	h := NewHub()
	ch, unsubscribe := h.Subscribe("")
	defer unsubscribe()

	base := time.Now().UTC()
	events := []domain.ActivityEvent{
		toolEvent("ao-7", base, domain.ActivityEventToolStart),
		toolEvent("ao-7", base.Add(time.Millisecond), domain.ActivityEventToolFailed),
		{SessionID: "ao-7", Kind: domain.ActivityEventActivity, At: base.Add(2 * time.Millisecond), Coarse: domain.CoarseWaiting},
		{SessionID: "ao-7", Kind: domain.ActivityEventMessage, At: base.Add(3 * time.Millisecond), Text: "hi",
			TTLMs: domain.DurationMs(domain.MessageDetailTTL)},
	}
	for _, ev := range events {
		if err := h.Publish(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}
	if got := drain(ch); len(got) != len(events) {
		t.Errorf("delivered %d events, want all %d: %+v", len(got), len(events), got)
	}
}

// An event published without a timestamp is stamped by the hub, so a consumer
// can always compute the decay window.
func TestHub_StampsMissingTimestamp(t *testing.T) {
	h := NewHub()
	fixed := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return fixed }
	ch, unsubscribe := h.Subscribe("")
	defer unsubscribe()

	if err := h.Publish(context.Background(), domain.ActivityEvent{SessionID: "ao-7", Kind: domain.ActivityEventActivity, Coarse: domain.CoarseIdle}); err != nil {
		t.Fatal(err)
	}
	got, _ := recv(t, ch)
	if !got.At.Equal(fixed) {
		t.Errorf("At = %v, want the hub clock %v", got.At, fixed)
	}
}

// A terminated session must not leave its throttle bookkeeping behind.
func TestHub_ForgetsExitedSessions(t *testing.T) {
	h := NewHub()
	ch, unsubscribe := h.Subscribe("")
	defer unsubscribe()

	base := time.Now().UTC()
	_ = h.Publish(context.Background(), toolEvent("ao-7", base, domain.ActivityEventToolStart))
	_ = h.Publish(context.Background(), domain.ActivityEvent{
		SessionID: "ao-7", Kind: domain.ActivityEventActivity, At: base.Add(time.Millisecond), Coarse: domain.CoarseExited,
	})
	drain(ch)

	h.mu.RLock()
	_, tracked := h.lastTool["ao-7"]
	h.mu.RUnlock()
	if tracked {
		t.Error("an exited session must be dropped from the throttle map")
	}
}

func drain(ch <-chan domain.ActivityEvent) []domain.ActivityEvent {
	var out []domain.ActivityEvent
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}
