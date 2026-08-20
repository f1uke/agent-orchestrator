// Package msgqueue holds messages addressed to a session that cannot receive
// them right now, and delivers them once the session is genuinely back.
//
// The gap it closes: a session the idle sweep SUSPENDED still has its record,
// its worktree and its stored runtime handle, but its tmux is gone. Handing
// that handle to the runtime types the message at a pane that no longer exists,
// which fails the send and loses the message - and the senders that hurt most
// are not humans. The comment nudge, the CI nudge and the smoke report-back fire
// once and move on.
//
// Two properties do the real work here:
//
//  1. DELIVERED ONCE, IN ORDER. Rows are ordered by the SQLite rowid, which is
//     insertion order and survives a restart. A deliverer must first win a
//     conditional pending -> delivering UPDATE, and the row is DELETED only
//     after the runtime accepted it, so "still in the table" and "not yet
//     delivered" are the same fact. A row still in flight when the daemon dies
//     is FAILED at the next boot rather than re-sent: a duplicate instruction in
//     an agent's transcript is worse than a visible drop.
//
//  2. DELIVERED WHEN THE AGENT IS LISTENING, not when the session is technically
//     back. "Listening" has two failure modes and the queue waits out both: a
//     permission prompt open in the pane (activity_state waiting_input) owns the
//     keyboard, so anything typed is eaten by the dialog and the trailing Enter can
//     answer it. And resuming a session recreates the pane as
//     `<shell> -c '<exports>; <agent argv>; exec <shell> -i'`, so for a moment
//     the foreground process is a SHELL. Text typed then is eaten by the shell
//     as a command line and the human is told it was delivered. The gate is
//     therefore the agent-liveness probe (ports.AgentLivenessProber: the pane
//     leader has a live child), and delivery additionally waits for that to hold
//     across readySettle so a flapping or crash-looping agent is not mistaken for
//     a ready one. A runtime without that capability falls back to a bounded
//     settle after the session is seen live again - stated plainly, because it is
//     a weaker guarantee.
package msgqueue

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// DefaultTTL is how long a held message stays worth delivering. A nudge that
	// arrives three days late acts on a world that has moved on - it is worse
	// than nothing, because the agent will act on it. One day covers an overnight
	// sleep and a weekend morning; past that the sender's context is stale and the
	// message is dropped visibly rather than delivered misleadingly.
	DefaultTTL = 24 * time.Hour
	// DefaultCap bounds one session's inbox. A session nobody ever opens must not
	// accumulate forever; at the bound the OLDEST pending message is evicted, so
	// what survives is the newest view of the world.
	DefaultCap = 20
	// DefaultReadySettle is how long the agent must have been continuously alive
	// before the first held message is typed at it. It is NOT a guess at boot
	// time - the readiness SIGNAL decides that - it only rejects an agent that
	// was alive one instant and gone the next.
	DefaultReadySettle = 3 * time.Second
	// DefaultLiveSettle is the fallback wait for a runtime that cannot probe
	// agent liveness at all (no ports.AgentLivenessProber). There is no signal to
	// wait for there, so this is an honest bounded guess and is deliberately much
	// longer than DefaultReadySettle.
	DefaultLiveSettle = 45 * time.Second
	// maxAttempts bounds redelivery of one message. Past it the row is failed
	// (kept, visible) instead of retried forever.
	maxAttempts = 5
)

// Store is the persistence the queue needs. *sqlite.Store satisfies it.
type Store interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	EnqueueSessionMessage(ctx context.Context, msg domain.QueuedMessage, limit int) (domain.QueuedMessage, int, error)
	ListQueuedMessages(ctx context.Context, id domain.SessionID) ([]domain.QueuedMessage, error)
	ListPendingQueuedMessages(ctx context.Context, id domain.SessionID) ([]domain.QueuedMessage, error)
	SessionsWithPendingMessages(ctx context.Context) ([]domain.SessionID, error)
	QueuedMessageCounts(ctx context.Context) (map[domain.SessionID]domain.QueuedMessageCounts, error)
	ClaimQueuedMessage(ctx context.Context, id int64, now time.Time) (bool, error)
	DeleteQueuedMessage(ctx context.Context, id int64) error
	ReleaseQueuedMessage(ctx context.Context, id int64, cause string, now time.Time) error
	FailQueuedMessage(ctx context.Context, id int64, cause string, now time.Time) error
	FailDeliveringQueuedMessages(ctx context.Context, cause string, now time.Time) (int, error)
	ExpirePendingQueuedMessages(ctx context.Context, now time.Time) ([]domain.QueuedMessage, error)
}

// Sender types a message into a session's pane. It is the same seam `ao send`
// uses for a live session, so a delivered queued message goes through the very
// same input gate and per-pane serialization.
type Sender interface {
	SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error
}

// Queue enqueues messages for sessions that cannot receive them and drains them
// once the session's agent is listening again.
type Queue struct {
	store  Store
	sender Sender
	// prober is the agent-liveness capability, nil when the runtime cannot
	// report it. Nil switches the readiness gate to the weaker live-settle
	// fallback documented on DefaultLiveSettle.
	prober ports.AgentLivenessProber
	logger *slog.Logger
	now    func() time.Time

	ttl         time.Duration
	inboxCap    int
	readySettle time.Duration
	liveSettle  time.Duration
	mu          sync.Mutex
	readySince  map[domain.SessionID]time.Time
}

// Option configures a Queue.
type Option func(*Queue)

// WithClock overrides the clock (tests).
func WithClock(now func() time.Time) Option { return func(q *Queue) { q.now = now } }

// WithTTL overrides how long a held message stays deliverable.
func WithTTL(d time.Duration) Option {
	return func(q *Queue) {
		if d > 0 {
			q.ttl = d
		}
	}
}

// WithCap overrides the per-session inbox bound.
func WithCap(n int) Option {
	return func(q *Queue) {
		if n > 0 {
			q.inboxCap = n
		}
	}
}

// WithSettle overrides both readiness settles (tests).
func WithSettle(ready, live time.Duration) Option {
	return func(q *Queue) {
		if ready > 0 {
			q.readySettle = ready
		}
		if live > 0 {
			q.liveSettle = live
		}
	}
}

// New builds a Queue. sender must be the same runtime `ao send` delivers
// through, and prober is the agent-liveness capability of the underlying
// runtime, which is the readiness signal.
//
// prober is passed EXPLICITLY rather than sniffed off sender, and that is
// load-bearing: the daemon delivers through a wrapper (the input-gated runtime)
// whose method set is the wrapped interface, so a type assertion on it silently
// finds no prober and the queue would quietly fall back to the weak timed wait.
// It cost a real verification run to notice. A nil prober means the runtime
// genuinely cannot report agent liveness (conpty), and only then is the bounded
// fallback correct.
func New(store Store, sender Sender, prober ports.AgentLivenessProber, logger *slog.Logger, opts ...Option) *Queue {
	if logger == nil {
		logger = slog.Default()
	}
	q := &Queue{
		store:       store,
		sender:      sender,
		logger:      logger,
		now:         func() time.Time { return time.Now().UTC() },
		ttl:         DefaultTTL,
		inboxCap:    DefaultCap,
		readySettle: DefaultReadySettle,
		liveSettle:  DefaultLiveSettle,
		readySince:  map[domain.SessionID]time.Time{},
	}
	q.prober = prober
	for _, opt := range opts {
		opt(q)
	}
	return q
}

// HasReadinessSignal reports whether this queue can see the agent itself come
// up, as opposed to falling back to a bounded wait after the session is back.
// Exposed so the daemon wiring can be pinned by a test: losing the signal is
// invisible at runtime (messages still arrive, just later and less safely).
func (q *Queue) HasReadinessSignal() bool { return q.prober != nil }

// Enqueue holds message for a session, returning the stored row and how many
// messages that session now has waiting. The caller reports both back to the
// sender, so "queued" is never indistinguishable from "delivered".
func (q *Queue) Enqueue(ctx context.Context, id domain.SessionID, body string) (domain.QueuedMessage, int, error) {
	now := q.now()
	msg := domain.QueuedMessage{
		SessionID: id,
		Body:      body,
		QueuedAt:  now,
		ExpiresAt: now.Add(q.ttl),
	}
	stored, pending, err := q.store.EnqueueSessionMessage(ctx, msg, q.inboxCap)
	if err != nil {
		return domain.QueuedMessage{}, 0, fmt.Errorf("queue message for %s: %w", id, err)
	}
	q.logger.Info("queued message for a session that cannot receive it now",
		"sessionID", id, "pending", pending, "expiresAt", stored.ExpiresAt)
	return stored, pending, nil
}

// List returns a session's whole inbox, oldest first.
func (q *Queue) List(ctx context.Context, id domain.SessionID) ([]domain.QueuedMessage, error) {
	return q.store.ListQueuedMessages(ctx, id)
}

// Counts returns per-session pending/failed totals for the read model.
func (q *Queue) Counts(ctx context.Context) (map[domain.SessionID]domain.QueuedMessageCounts, error) {
	return q.store.QueuedMessageCounts(ctx)
}

// RecoverInFlight fails every message that was mid-delivery when the previous
// daemon died. Call once at boot, before the first Drain.
func (q *Queue) RecoverInFlight(ctx context.Context) (int, error) {
	n, err := q.store.FailDeliveringQueuedMessages(ctx, "daemon restarted while this message was being delivered; not re-sent to avoid delivering it twice", q.now())
	if err != nil {
		return 0, err
	}
	if n > 0 {
		q.logger.Warn("failed in-flight queued messages after a restart", "count", n)
	}
	return n, nil
}

// Drain is one sweep: expire what is too old to be useful, then deliver what is
// waiting for a session whose agent is listening again. Per-session failures are
// logged and never abort the sweep.
func (q *Queue) Drain(ctx context.Context) error {
	if expired, err := q.store.ExpirePendingQueuedMessages(ctx, q.now()); err != nil {
		q.logger.Error("message queue: expiry sweep failed", "error", err)
	} else {
		for _, msg := range expired {
			q.logger.Warn("dropped a queued message that outlived its usefulness",
				"sessionID", msg.SessionID, "queuedAt", msg.QueuedAt, "expiresAt", msg.ExpiresAt)
		}
	}
	ids, err := q.store.SessionsWithPendingMessages(ctx)
	if err != nil {
		return fmt.Errorf("message queue: list sessions with pending messages: %w", err)
	}
	for _, id := range ids {
		if err := q.drainSession(ctx, id); err != nil {
			q.logger.Error("message queue: delivery failed, skipping session", "sessionID", id, "error", err)
		}
	}
	return nil
}

// drainSession delivers one session's waiting messages, in order, if and only if
// its agent is listening.
func (q *Queue) drainSession(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := q.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		// The session row is gone (deleted); its messages cannot be delivered and
		// the FK cascade has already removed them. Nothing to do.
		q.forget(id)
		return nil
	}
	if rec.IsTerminated {
		// A session that ENDED while its messages waited will not come back on its
		// own. Fail them so the drop is visible rather than pending forever.
		return q.failAllPending(ctx, id, "session ended before this message could be delivered")
	}
	if rec.IsSuspended {
		q.forget(id) // still asleep: readiness starts over when it wakes
		return nil
	}
	if !rec.Activity.State.IsListening() {
		// The pane is up but a permission prompt owns the keyboard, so the agent is
		// not listening — the same fact this queue already waits for when a session
		// is booting, arriving from the activity feed instead of the liveness probe.
		// Typing now would feed the dialog and the trailing Enter could answer it.
		// Readiness starts over when the prompt clears, exactly as it does after a
		// wake, so nothing lands the instant the human hits "allow".
		q.forget(id)
		return nil
	}
	handle := ports.RuntimeHandle{ID: strings.TrimSpace(rec.Metadata.RuntimeHandleID)}
	if handle.ID == "" {
		return nil
	}
	ready, err := q.ready(ctx, id, handle)
	if err != nil {
		return err
	}
	if !ready {
		return nil // still coming up: the next sweep looks again
	}
	msgs, err := q.store.ListPendingQueuedMessages(ctx, id)
	if err != nil {
		return err
	}
	for _, msg := range msgs {
		delivered, err := q.deliver(ctx, handle, msg)
		if err != nil {
			return err
		}
		if !delivered {
			// Stop at the first message that did not land: delivering the next one
			// would reorder this session's inbox.
			return nil
		}
	}
	return nil
}

// deliver sends one message and settles its row. It reports whether the message
// actually reached the pane, so the caller can stop rather than reorder.
func (q *Queue) deliver(ctx context.Context, handle ports.RuntimeHandle, msg domain.QueuedMessage) (bool, error) {
	now := q.now()
	won, err := q.store.ClaimQueuedMessage(ctx, msg.ID, now)
	if err != nil {
		return false, err
	}
	if !won {
		// Another deliverer holds it. Not an error, but this sweep must not run
		// past it or the inbox would be reordered.
		return false, nil
	}
	if err := q.sender.SendMessage(ctx, handle, decorate(msg, now)); err != nil {
		cause := fmt.Sprintf("delivery failed: %v", err)
		if msg.Attempts+1 >= maxAttempts {
			q.logger.Error("giving up on a queued message", "sessionID", msg.SessionID, "attempts", msg.Attempts+1, "error", err)
			return false, q.store.FailQueuedMessage(ctx, msg.ID, cause, now)
		}
		return false, q.store.ReleaseQueuedMessage(ctx, msg.ID, cause, now)
	}
	if err := q.store.DeleteQueuedMessage(ctx, msg.ID); err != nil {
		return false, err
	}
	q.logger.Info("delivered a queued message", "sessionID", msg.SessionID, "heldFor", now.Sub(msg.QueuedAt).Round(time.Second))
	return true, nil
}

// failAllPending marks every waiting message for a session as undeliverable.
func (q *Queue) failAllPending(ctx context.Context, id domain.SessionID, cause string) error {
	msgs, err := q.store.ListPendingQueuedMessages(ctx, id)
	if err != nil {
		return err
	}
	now := q.now()
	for _, msg := range msgs {
		if err := q.store.FailQueuedMessage(ctx, msg.ID, cause, now); err != nil {
			return err
		}
		q.logger.Warn("a queued message will never be delivered", "sessionID", id, "queuedAt", msg.QueuedAt, "reason", cause)
	}
	q.forget(id)
	return nil
}

// ready reports whether the session's AGENT is listening - not merely whether
// the session is back. See the package doc for why the difference is the whole
// feature.
func (q *Queue) ready(ctx context.Context, id domain.SessionID, handle ports.RuntimeHandle) (bool, error) {
	settle := q.readySettle
	if q.prober == nil {
		// No readiness signal available on this runtime: the honest fallback is a
		// bounded wait after the session is back, not a claim we cannot support.
		settle = q.liveSettle
	} else {
		alive, err := q.prober.AgentAlive(ctx, handle)
		if err != nil {
			// A failed probe is not proof of readiness OR of death: hold the
			// messages and look again next sweep.
			return false, fmt.Errorf("agent liveness probe: %w", err)
		}
		if !alive {
			q.forget(id)
			return false, nil
		}
	}
	first := q.observe(id)
	return q.now().Sub(first) >= settle, nil
}

// observe records (once) when this session was first seen ready, and returns
// that instant. The settle is measured from it, so readiness must HOLD rather
// than merely flicker true.
func (q *Queue) observe(id domain.SessionID) time.Time {
	q.mu.Lock()
	defer q.mu.Unlock()
	if at, ok := q.readySince[id]; ok {
		return at
	}
	at := q.now()
	q.readySince[id] = at
	return at
}

// forget drops a session's readiness observation, so a session that goes away
// again must re-earn the settle before anything is typed at it.
func (q *Queue) forget(id domain.SessionID) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.readySince, id)
}

// decorate prefixes a held message with when it was queued and how long it
// waited. A message delivered hours after it was written describes a world that
// has moved on; the reader - agent or human - has to be able to see that
// without asking.
func decorate(msg domain.QueuedMessage, now time.Time) string {
	held := now.Sub(msg.QueuedAt)
	if held < 0 {
		held = 0
	}
	return fmt.Sprintf("[AO queued %s (%s ago), held while this session was asleep]\n%s",
		msg.QueuedAt.UTC().Format(time.RFC3339), roundHeld(held), msg.Body)
}

// roundHeld renders a held duration at a resolution a reader can act on:
// seconds under a minute, minutes under an hour, then whole hours.
func roundHeld(d time.Duration) string {
	switch {
	case d < time.Minute:
		return d.Round(time.Second).String()
	case d < time.Hour:
		return d.Round(time.Minute).String()
	default:
		return d.Round(time.Hour).String()
	}
}
