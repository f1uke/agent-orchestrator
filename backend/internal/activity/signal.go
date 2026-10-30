package activity

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Recorder applies an activity signal to a session. *lifecycle.Manager is one.
type Recorder interface {
	ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error
}

// Publisher fans a feed event out to live subscribers. *Hub is one.
type Publisher interface {
	Publish(ctx context.Context, ev domain.ActivityEvent) error
}

// Signal applies s to the session, then publishes it to feed. It publishes
// only a signal the recorder accepted, because the feed reports facts.
// Publishing is lossy and never fails the call; a nil feed publishes nothing.
func Signal(ctx context.Context, rec Recorder, feed Publisher, id domain.SessionID, s ports.ActivitySignal, detail *domain.ActivityDetail) error {
	if err := rec.ApplyActivitySignal(ctx, id, s); err != nil {
		return err
	}
	if feed != nil {
		_ = feed.Publish(ctx, eventFromSignal(id, s.State, detail))
	}
	return nil
}

// eventFromSignal builds the feed frame for an accepted activity signal.
//
// The whole truth contract lives in this function. Every frame carries the
// coarse level the signal implies AND how long that level stays true; a frame
// that also carries curated detail carries the (much shorter) window in which
// that detail may be shown as currently happening. A consumer therefore cannot
// render a finished action as live: the detail expires and it falls back to the
// coarse level, then to "unknown".
//
// A detail whose kind AO does not recognise, or that arrives over-long or
// secret-shaped, is clamped or dropped here - the per-tool whitelist in the hook
// process is the primary guard, this is the backstop.
func eventFromSignal(id domain.SessionID, state domain.ActivityState, detail *domain.ActivityDetail) domain.ActivityEvent {
	coarse, coarseTTL := domain.CoarseFromActivityState(state)
	ev := domain.ActivityEvent{
		SessionID:   id,
		Kind:        domain.ActivityEventActivity,
		At:          time.Now().UTC(),
		Coarse:      coarse,
		CoarseTTLMs: domain.DurationMs(coarseTTL),
	}
	if detail == nil {
		return ev
	}
	safe, ok := detail.SanitizedForFeed()
	if !ok {
		return ev
	}
	ev.Kind = safe.Kind
	ev.Tool = safe.Tool
	ev.Target = safe.Target
	ev.Text = safe.Text
	ev.TTLMs = domain.DurationMs(domain.DetailTTL(safe.Kind))
	return ev
}
