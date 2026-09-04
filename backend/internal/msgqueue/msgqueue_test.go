package msgqueue_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgdelivery"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgqueue"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// The tests below run against the REAL SQLite store rather than a fake, because
// the two properties under test - delivered once and never reordered - live in
// the SQL (a conditional claim and the rowid order), not in Go. A fake store
// would happily agree with itself.

const testHandle = "lab-worker-1"

// sender is a runtime that records what it typed and can report agent liveness,
// which is the readiness signal the queue gates on.
type sender struct {
	mu       sync.Mutex
	sent     []string
	failNext int // fail this many sends before succeeding
	alive    bool
	aliveErr error
	probes   int
	origins  []msgdelivery.Origin
}

func (s *sender) SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.origins = append(s.origins, msgdelivery.OriginOf(ctx))
	if handle.ID != testHandle {
		return errors.New("can't find pane: " + handle.ID)
	}
	if s.failNext > 0 {
		s.failNext--
		return errors.New("pane refused the send")
	}
	s.sent = append(s.sent, message)
	return nil
}

func (s *sender) AgentAlive(_ context.Context, _ ports.RuntimeHandle) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probes++
	if s.aliveErr != nil {
		return false, s.aliveErr
	}
	return s.alive, nil
}

func (s *sender) delivered() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

func (s *sender) lastOrigin() msgdelivery.Origin {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.origins) == 0 {
		return msgdelivery.Origin{}
	}
	return s.origins[len(s.origins)-1]
}

func (s *sender) setAlive(alive bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alive = alive
}

// blindSender is a runtime with NO agent-liveness capability, which is what
// forces the queue onto its weaker bounded-settle fallback.
type blindSender struct{ sender }

func (b *blindSender) AgentAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	panic("blindSender must not be probed")
}

// bodiesOf strips the age banner so a test can assert on what was actually sent.
func bodiesOf(msgs []string) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if i := strings.Index(m, "\n"); i >= 0 {
			m = m[i+1:]
		}
		out = append(out, m)
	}
	return out
}

type harness struct {
	store   *sqlite.Store
	queue   *msgqueue.Queue
	sender  *sender
	session domain.SessionID
	now     time.Time
}

func (h *harness) advance(d time.Duration) { h.now = h.now.Add(d) }

// setSession rewrites the session's live/suspended flags the way the lifecycle
// reducer would.
func (h *harness) setSession(t *testing.T, mutate func(*domain.SessionRecord)) {
	t.Helper()
	rec, ok, err := h.store.GetSession(context.Background(), h.session)
	if err != nil || !ok {
		t.Fatalf("get session: ok=%v err=%v", ok, err)
	}
	mutate(&rec)
	if err := h.store.UpdateSession(context.Background(), rec); err != nil {
		t.Fatalf("update session: %v", err)
	}
}

func newHarness(t *testing.T, opts ...msgqueue.Option) *harness {
	t.Helper()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Date(2031, 3, 4, 9, 0, 0, 0, time.UTC)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "lab", Path: "/tmp/lab", RegisteredAt: now}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	rec, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "lab",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		Metadata:  domain.SessionMetadata{Branch: "lab/x", WorkspacePath: "/ws", RuntimeHandleID: testHandle},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	h := &harness{store: store, sender: &sender{}, session: rec.ID, now: now}
	base := []msgqueue.Option{
		msgqueue.WithClock(func() time.Time { return h.now }),
		msgqueue.WithSettle(10*time.Second, time.Minute),
	}
	h.queue = msgqueue.New(store, h.sender, h.sender, quietLogger(), append(base, opts...)...)
	return h
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// suspend puts the session where this whole feature starts: paused, tmux gone,
// record and handle intact.
func (h *harness) suspend(t *testing.T) {
	h.setSession(t, func(rec *domain.SessionRecord) { rec.IsSuspended = true })
}

func (h *harness) wake(t *testing.T) {
	h.setSession(t, func(rec *domain.SessionRecord) { rec.IsSuspended = false })
}

func (h *harness) enqueue(t *testing.T, body string) {
	t.Helper()
	if _, _, err := h.queue.Enqueue(context.Background(), h.session, body); err != nil {
		t.Fatalf("enqueue %q: %v", body, err)
	}
}

func (h *harness) drain(t *testing.T) {
	t.Helper()
	if err := h.queue.Drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
}

// readyAndDrain walks the session from "just woken" to "delivered": the agent
// comes up, one sweep observes it, the settle passes, the next sweep delivers.
func (h *harness) readyAndDrain(t *testing.T) {
	t.Helper()
	h.sender.setAlive(true)
	h.drain(t)
	h.advance(11 * time.Second)
	h.drain(t)
}

func TestAMessageForASleepingSessionIsHeldNotLost(t *testing.T) {
	h := newHarness(t)
	h.suspend(t)
	msg, pending, err := h.queue.Enqueue(context.Background(), h.session, "look at the failing check")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if pending != 1 {
		t.Fatalf("pending = %d, want 1", pending)
	}
	if msg.ExpiresAt.Before(msg.QueuedAt) {
		t.Fatalf("expiry %s must be after queued-at %s", msg.ExpiresAt, msg.QueuedAt)
	}
	// The sweep must not type at a suspended session: its pane is gone.
	h.sender.setAlive(true)
	h.drain(t)
	h.advance(time.Minute)
	h.drain(t)
	if got := h.sender.delivered(); len(got) != 0 {
		t.Fatalf("delivered %v to a suspended session, want nothing", got)
	}
	held, err := h.queue.List(context.Background(), h.session)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(held) != 1 || held[0].State != domain.QueuedMessagePending {
		t.Fatalf("inbox = %+v, want one pending message still held", held)
	}
}

func TestAHeldMessageIsDeliveredOnceTheAgentIsListening(t *testing.T) {
	h := newHarness(t)
	h.suspend(t)
	h.enqueue(t, "look at the failing check")
	h.wake(t)

	// The session is back but the pane is still running the shell, not the agent:
	// typing now would hand the text to the shell as a command line.
	h.sender.setAlive(false)
	h.drain(t)
	if got := h.sender.delivered(); len(got) != 0 {
		t.Fatalf("delivered %v before the agent was alive", got)
	}

	// The agent is up, but readiness has only just been observed: one flicker is
	// not proof, so the settle must still hold it.
	h.sender.setAlive(true)
	h.drain(t)
	if got := h.sender.delivered(); len(got) != 0 {
		t.Fatalf("delivered %v before the readiness settle elapsed", got)
	}

	h.advance(11 * time.Second)
	h.drain(t)
	got := h.sender.delivered()
	if len(got) != 1 {
		t.Fatalf("delivered %v, want exactly the held message", got)
	}
	if b := bodiesOf(got); b[0] != "look at the failing check" {
		t.Fatalf("delivered body = %q, want the original message", b[0])
	}
}

func TestAHeldMessageIsDeliveredExactlyOnce(t *testing.T) {
	h := newHarness(t)
	h.suspend(t)
	h.enqueue(t, "only once please")
	h.wake(t)
	h.readyAndDrain(t)

	// Three more sweeps: a delivered message is gone from the queue, so nothing
	// can re-send it.
	for i := 0; i < 3; i++ {
		h.advance(time.Second)
		h.drain(t)
	}
	if got := h.sender.delivered(); len(got) != 1 {
		t.Fatalf("delivered %d times: %v, want exactly 1", len(got), got)
	}
	held, err := h.queue.List(context.Background(), h.session)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("inbox = %+v after delivery, want empty", held)
	}
}

func TestHeldMessagesAreDeliveredInTheOrderTheyWereSent(t *testing.T) {
	h := newHarness(t)
	h.suspend(t)
	for _, body := range []string{"first", "second", "third"} {
		h.enqueue(t, body)
		h.advance(time.Second)
	}
	h.wake(t)
	h.readyAndDrain(t)

	want := []string{"first", "second", "third"}
	got := bodiesOf(h.sender.delivered())
	if len(got) != 3 {
		t.Fatalf("delivered %v, want all three", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("delivery order = %v, want %v", got, want)
		}
	}
}

func TestAFailedDeliveryDoesNotLetLaterMessagesOvertakeIt(t *testing.T) {
	h := newHarness(t)
	h.suspend(t)
	h.enqueue(t, "first")
	h.enqueue(t, "second")
	h.wake(t)
	h.sender.failNext = 1

	h.readyAndDrain(t)
	if got := h.sender.delivered(); len(got) != 0 {
		t.Fatalf("delivered %v while the first message was failing, want nothing (order would break)", got)
	}
	// Next sweep: the first message retries and both land, still in order.
	h.advance(time.Second)
	h.drain(t)
	got := bodiesOf(h.sender.delivered())
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("delivered %v, want [first second]", got)
	}
}

func TestAMessageThatKeepsFailingIsGivenUpOnVisibly(t *testing.T) {
	h := newHarness(t)
	h.suspend(t)
	h.enqueue(t, "doomed")
	h.wake(t)
	h.sender.failNext = 99

	h.readyAndDrain(t)
	for i := 0; i < 6; i++ {
		h.advance(time.Second)
		h.drain(t)
	}
	held, err := h.queue.List(context.Background(), h.session)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("inbox = %+v, want the failed message kept so the drop is visible", held)
	}
	if held[0].State != domain.QueuedMessageFailed {
		t.Fatalf("state = %s after repeated failures, want failed", held[0].State)
	}
	if held[0].LastError == "" {
		t.Fatal("a failed message must record why it was given up on")
	}
}

func TestAnAgentThatDiesAgainMustReEarnTheSettle(t *testing.T) {
	h := newHarness(t)
	h.suspend(t)
	h.enqueue(t, "careful now")
	h.wake(t)

	h.sender.setAlive(true)
	h.drain(t) // first observation
	h.advance(5 * time.Second)
	h.sender.setAlive(false)
	h.drain(t) // the agent went away: readiness resets
	h.advance(6 * time.Second)
	h.sender.setAlive(true)
	h.drain(t) // fresh observation, settle starts over
	if got := h.sender.delivered(); len(got) != 0 {
		t.Fatalf("delivered %v to an agent that had just crashed and come back, want nothing yet", got)
	}
	h.advance(11 * time.Second)
	h.drain(t)
	if got := h.sender.delivered(); len(got) != 1 {
		t.Fatalf("delivered %v after the settle held, want the message", got)
	}
}

func TestAFailedLivenessProbeHoldsTheMessage(t *testing.T) {
	h := newHarness(t)
	h.suspend(t)
	h.enqueue(t, "hold me")
	h.wake(t)
	h.sender.aliveErr = errors.New("probe blew up")

	// Drain reports per-session failures through the log, not the return value:
	// one broken session must not stop the sweep for the others.
	h.drain(t)
	h.advance(time.Minute)
	h.drain(t)
	if got := h.sender.delivered(); len(got) != 0 {
		t.Fatalf("delivered %v on an ambiguous probe, want nothing", got)
	}
}

func TestARuntimeThatCannotProbeFallsBackToABoundedWait(t *testing.T) {
	h := newHarness(t)
	blind := &blindSender{}
	now := h.now
	q := msgqueue.New(h.store, blind, nil, quietLogger(),
		msgqueue.WithClock(func() time.Time { return now }),
		msgqueue.WithSettle(10*time.Second, time.Minute))
	ctx := context.Background()
	h.suspend(t)
	if _, _, err := q.Enqueue(ctx, h.session, "no probe here"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	h.wake(t)
	if err := q.Drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := blind.delivered(); len(got) != 0 {
		t.Fatalf("delivered %v immediately, want the fallback wait to hold it", got)
	}
	now = now.Add(90 * time.Second)
	if err := q.Drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := blind.delivered(); len(got) != 1 {
		t.Fatalf("delivered %v after the fallback wait, want the message", got)
	}
}

func TestAMessageThatOutlivedItsUsefulnessIsDroppedNotDelivered(t *testing.T) {
	h := newHarness(t, msgqueue.WithTTL(time.Hour))
	h.suspend(t)
	h.enqueue(t, "the CI check you were looking at is failing")
	h.wake(t)

	h.advance(2 * time.Hour)
	h.readyAndDrain(t)
	if got := h.sender.delivered(); len(got) != 0 {
		t.Fatalf("delivered %v three hours late, want it dropped", got)
	}
	held, err := h.queue.List(context.Background(), h.session)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("inbox = %+v, want the expired message removed", held)
	}
}

func TestOneSessionsInboxIsBounded(t *testing.T) {
	h := newHarness(t, msgqueue.WithCap(3))
	h.suspend(t)
	for _, body := range []string{"one", "two", "three", "four", "five"} {
		h.enqueue(t, body)
	}
	held, err := h.queue.List(context.Background(), h.session)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(held) != 3 {
		t.Fatalf("inbox = %d messages, want 3 (the cap)", len(held))
	}
	h.wake(t)
	h.readyAndDrain(t)
	got := bodiesOf(h.sender.delivered())
	want := []string{"three", "four", "five"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("delivered %v, want the newest three %v", got, want)
		}
	}
}

func TestADeliveredMessageSaysHowLongItWaited(t *testing.T) {
	h := newHarness(t)
	h.suspend(t)
	h.enqueue(t, "the review thread is still open")
	queuedAt := h.now
	h.advance(3 * time.Hour)
	h.wake(t)
	h.readyAndDrain(t)

	got := h.sender.delivered()
	if len(got) != 1 {
		t.Fatalf("delivered %v, want one message", got)
	}
	banner := got[0][:strings.Index(got[0], "\n")]
	if !strings.Contains(banner, queuedAt.Format(time.RFC3339)) {
		t.Fatalf("banner %q must carry when the message was queued (%s)", banner, queuedAt.Format(time.RFC3339))
	}
	if !strings.Contains(banner, "3h") {
		t.Fatalf("banner %q must say how long it waited", banner)
	}
	if !strings.Contains(got[0], "the review thread is still open") {
		t.Fatalf("delivered message %q lost its body", got[0])
	}
}

func TestMessagesForASessionThatEndedAreGivenUpOn(t *testing.T) {
	h := newHarness(t)
	h.suspend(t)
	h.enqueue(t, "never arriving")
	h.setSession(t, func(rec *domain.SessionRecord) {
		rec.IsSuspended = false
		rec.IsTerminated = true
	})
	h.sender.setAlive(true)
	h.drain(t)

	held, err := h.queue.List(context.Background(), h.session)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(held) != 1 || held[0].State != domain.QueuedMessageFailed {
		t.Fatalf("inbox = %+v, want the message failed rather than pending forever", held)
	}
	if got := h.sender.delivered(); len(got) != 0 {
		t.Fatalf("delivered %v to a terminated session", got)
	}
}

func TestAnInFlightMessageIsNotResentAfterARestart(t *testing.T) {
	h := newHarness(t)
	h.suspend(t)
	h.enqueue(t, "in flight when the lights went out")
	h.wake(t)
	// Simulate the crash window: the row is claimed, and the process dies before
	// the runtime call settles.
	pending, err := h.store.ListPendingQueuedMessages(context.Background(), h.session)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if won, err := h.store.ClaimQueuedMessage(context.Background(), pending[0].ID, h.now); err != nil || !won {
		t.Fatalf("claim: (%v, %v)", won, err)
	}

	n, err := h.queue.RecoverInFlight(context.Background())
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if n != 1 {
		t.Fatalf("recovered %d rows, want 1", n)
	}
	h.readyAndDrain(t)
	if got := h.sender.delivered(); len(got) != 0 {
		t.Fatalf("re-sent %v after a restart; a duplicate instruction is worse than a visible drop", got)
	}
	held, err := h.queue.List(context.Background(), h.session)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(held) != 1 || held[0].State != domain.QueuedMessageFailed || held[0].LastError == "" {
		t.Fatalf("inbox = %+v, want the in-flight message failed with a reason", held)
	}
}

func TestCountsReportWhatIsWaiting(t *testing.T) {
	h := newHarness(t)
	h.suspend(t)
	h.enqueue(t, "one")
	h.enqueue(t, "two")
	counts, err := h.queue.Counts(context.Background())
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts[h.session].Pending != 2 {
		t.Fatalf("counts = %+v, want 2 pending for %s", counts, h.session)
	}
}

// atPrompt / leavePrompt move the session in and out of the state a permission
// dialog puts it in: the pane is LIVE and the agent process is alive, but the
// keyboard belongs to the dialog.
func (h *harness) atPrompt(t *testing.T) {
	h.setSession(t, func(rec *domain.SessionRecord) {
		rec.Activity = domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: h.now}
	})
}

func (h *harness) leavePrompt(t *testing.T, state domain.ActivityState) {
	h.setSession(t, func(rec *domain.SessionRecord) {
		rec.Activity = domain.Activity{State: state, LastActivityAt: h.now}
	})
}

// A live, alive session whose agent is sitting at a permission prompt is NOT
// listening: text typed now is consumed by the dialog and the trailing Enter can
// answer it. The queue holds, which is the whole reason lifecycle is allowed to
// hand it a nudge for such a session instead of dropping one.
func TestAMessageIsHeldWhileTheAgentSitsAtAPrompt(t *testing.T) {
	h := newHarness(t)
	h.sender.setAlive(true)
	h.atPrompt(t)
	h.enqueue(t, "CI is failing")

	h.drain(t)
	h.advance(11 * time.Second)
	h.drain(t)

	if got := h.sender.delivered(); len(got) != 0 {
		t.Fatalf("delivered %v into an open permission prompt", got)
	}
	pending, err := h.queue.List(context.Background(), h.session)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("held = %d messages, want the nudge still waiting", len(pending))
	}
}

// Once the prompt is answered the agent is listening again and the held nudge
// lands. Readiness restarts from scratch, exactly as it does after a wake, so
// nothing is typed the instant the human hits "allow".
func TestAHeldMessageLandsOnceThePromptIsAnswered(t *testing.T) {
	for _, state := range []domain.ActivityState{domain.ActivityActive, domain.ActivityIdle, domain.ActivityParked} {
		t.Run(string(state), func(t *testing.T) {
			h := newHarness(t)
			h.sender.setAlive(true)
			h.atPrompt(t)
			h.enqueue(t, "CI is failing")
			h.drain(t)

			h.leavePrompt(t, state)
			h.drain(t)
			if got := h.sender.delivered(); len(got) != 0 {
				t.Fatalf("delivered %v before the settle elapsed", got)
			}
			h.advance(11 * time.Second)
			h.drain(t)

			got := bodiesOf(h.sender.delivered())
			if len(got) != 1 || got[0] != "CI is failing" {
				t.Fatalf("delivered = %v, want the held nudge once the agent was listening", got)
			}
		})
	}
}

// A held message is delivered minutes or hours after it was sent, at whoever is
// at the keyboard by then - which makes it exactly the delivery whose path
// nobody can reconstruct afterwards. The queue names itself so the record can
// tell a drain apart from somebody's `ao send`.
func TestADrainedMessageSaysADrainDeliveredIt(t *testing.T) {
	h := newHarness(t)
	h.suspend(t)
	h.enqueue(t, "held, then delivered")
	h.wake(t)
	h.sender.setAlive(true)
	h.drain(t)
	h.advance(11 * time.Second)
	h.drain(t)

	if got := h.sender.delivered(); len(got) != 1 {
		t.Fatalf("delivered %v, want the held message", got)
	}
	origin := h.sender.lastOrigin()
	if origin.Trigger != msgdelivery.TriggerQueueDrain {
		t.Fatalf("trigger = %q, want the queue to name itself", origin.Trigger)
	}
	if origin.Session != string(h.session) {
		t.Fatalf("session = %q, want %q", origin.Session, h.session)
	}
}
