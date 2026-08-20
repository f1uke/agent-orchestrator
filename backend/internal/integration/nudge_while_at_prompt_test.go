// This file is the end-to-end regression guard for the dropped-nudge bug: an
// actionable nudge produced while a session sat in waiting_input was not merely
// delayed, it was lost for good.
//
// The loss had two halves and both are exercised here against real components -
// a real sqlite.Store, a real lifecycle.Manager, a real observe/scm.Observer and
// a real msgqueue.Queue:
//
//  1. The lifecycle reducers returned before the messenger was called, so nothing
//     was sent and nothing was held.
//  2. The observer treats a nil return from ApplySCMObservation as "delivered"
//     and immediately stamps the observation's semantic hashes as acknowledged.
//     With the facts unchanged afterwards, `!prepared.Changed.*` short-circuits
//     every later poll before lifecycle is reached - so the nudge never came back
//     even once the session was free.
//
// Only the ~15-line messenger below is a stand-in, and it is the same shape as
// daemon.runtimeMessenger (which has its own direct coverage in
// daemon/wiring_test.go): deliver to a live pane, or hand it to the queue.
package integration

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgqueue"
	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

const promptFixtureHandle = "ao-1/terminal_0"

// paneSpy is the runtime seam: it records what was typed into the session's pane
// and reports the agent as alive, so the queue's readiness gate is satisfied and
// the only thing that can hold a message back is the prompt itself.
type paneSpy struct {
	mu   sync.Mutex
	sent []string
}

func (p *paneSpy) SendMessage(_ context.Context, _ ports.RuntimeHandle, message string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, message)
	return nil
}

func (p *paneSpy) AgentAlive(context.Context, ports.RuntimeHandle) (bool, error) { return true, nil }

func (p *paneSpy) typed() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.sent...)
}

// queueingMessenger mirrors daemon.runtimeMessenger: type at the live pane, or
// hand the message to the queue when the session cannot receive it.
type queueingMessenger struct {
	store *sqlite.Store
	pane  *paneSpy
	queue *msgqueue.Queue
}

func (m queueingMessenger) Send(ctx context.Context, id domain.SessionID, message string) (ports.SendOutcome, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return ports.SendOutcome{}, err
	}
	if !rec.CanReceiveMessage() {
		stored, pending, qErr := m.queue.Enqueue(ctx, id, message)
		if qErr != nil {
			return ports.SendOutcome{}, qErr
		}
		return ports.SendOutcome{Queued: true, QueuedAt: stored.QueuedAt, Pending: pending}, nil
	}
	return ports.SendOutcome{}, m.pane.SendMessage(ctx, ports.RuntimeHandle{ID: promptFixtureHandle}, message)
}

type promptFixture struct {
	store    *sqlite.Store
	lcm      *lifecycle.Manager
	queue    *msgqueue.Queue
	pane     *paneSpy
	provider *cannedSCMProvider
	observer *scmobserve.Observer
	session  domain.SessionRecord
	now      time.Time
}

func (f *promptFixture) setActivity(t *testing.T, state domain.ActivityState) {
	t.Helper()
	if err := f.lcm.ApplyActivitySignal(context.Background(), f.session.ID, ports.ActivitySignal{
		Valid: true, State: state, Timestamp: f.now,
	}); err != nil {
		t.Fatalf("ApplyActivitySignal(%s): %v", state, err)
	}
}

// drain runs the queue sweep twice with the readiness settle in between, which
// is how the daemon's ticker gets a held message delivered.
func (f *promptFixture) drain(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := f.queue.Drain(ctx); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	f.now = f.now.Add(2 * time.Second)
	if err := f.queue.Drain(ctx); err != nil {
		t.Fatalf("Drain: %v", err)
	}
}

func newPromptFixture(t *testing.T) *promptFixture {
	t.Helper()
	ctx := context.Background()

	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "octo", Path: t.TempDir(), DisplayName: "octo",
		RepoOriginURL: scmTestOriginURL, RegisteredAt: now,
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	sess, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "octo", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata:  domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/ws/octo", RuntimeHandleID: promptFixtureHandle},
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	f := &promptFixture{store: store, session: sess, now: now, pane: &paneSpy{}}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	f.queue = msgqueue.New(store, f.pane, f.pane, quiet,
		msgqueue.WithClock(func() time.Time { return f.now }),
		msgqueue.WithSettle(time.Second, time.Second),
	)
	f.lcm = lifecycle.New(store, queueingMessenger{store: store, pane: f.pane, queue: f.queue},
		lifecycle.WithNotificationSink(newSCMNotifier(store, now)))
	f.provider = newCannedSCMProvider()
	f.observer = scmobserve.New(f.provider, store, f.lcm, scmobserve.Config{
		Tick:   time.Hour,
		Clock:  func() time.Time { return f.now },
		Logger: quiet,
	})
	return f
}

// TestCIFailureReachesAnAgentThatWasAtAPromptWhenItBroke walks the exact sequence
// the bug report describes. Before the fix the final assertion found zero
// messages: the nudge was dropped at the gate AND the observation was
// acknowledged, so no later poll ever offered it again.
func TestCIFailureReachesAnAgentThatWasAtAPromptWhenItBroke(t *testing.T) {
	ctx := context.Background()
	f := newPromptFixture(t)
	const (
		prURL   = "https://github.com/octocat/hello/pull/42"
		headSHA = "deadbeef"
		logTail = "FAILED: build broke\n"
	)

	// The agent hits a permission prompt. Its Notification hook POSTs
	// waiting_input through the same entry point the HTTP controller uses.
	f.setActivity(t, domain.ActivityWaitingInput)

	// CI goes red while the human is away.
	f.provider.detected["feat/x"] = ports.SCMPRObservation{
		URL: prURL, Number: 42, SourceBranch: "feat/x", HeadRepo: scmTestRepo.Repo, TargetBranch: "main", HeadSHA: headSHA,
	}
	f.provider.observations[42] = failingSCMObservation(prURL, 42, headSHA, logTail)
	if err := f.observer.Poll(ctx); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	// Nothing may be typed at a pane with an open permission dialog...
	f.drain(t)
	if got := f.pane.typed(); len(got) != 0 {
		t.Fatalf("typed %v into an open permission prompt", got)
	}
	// ...but the nudge must be HELD, not dropped. This is what #217's queue is for
	// and what the reducers' early return kept it from ever seeing.
	held, err := f.queue.List(ctx, f.session.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("held = %d messages, want the CI nudge waiting", len(held))
	}

	// The human answers the prompt; the agent's next hook reports it working again.
	f.setActivity(t, domain.ActivityActive)
	f.drain(t)

	typed := f.pane.typed()
	if len(typed) != 1 {
		t.Fatalf("pane received %d messages after the prompt cleared, want the CI nudge: %v", len(typed), typed)
	}
	if !strings.Contains(typed[0], logTail) {
		t.Fatalf("delivered message %q does not carry the failing check's log tail", typed[0])
	}
}

// A PARKED agent has nothing open and is not blocked on anyone, so its nudge is
// typed straight in - no queue, no wait. This is the state split earning its
// keep: before it, the only sticky "not working" reading was waiting_input, and
// routing a parked agent through it suppressed exactly the message it needed.
func TestCIFailureReachesAParkedAgentImmediately(t *testing.T) {
	ctx := context.Background()
	f := newPromptFixture(t)
	const (
		prURL   = "https://github.com/octocat/hello/pull/43"
		headSHA = "cafebabe"
		logTail = "FAILED: vet is unhappy\n"
	)

	// Claude Code's Notification(idle_prompt) after the turn ended.
	f.setActivity(t, domain.ActivityParked)

	f.provider.detected["feat/x"] = ports.SCMPRObservation{
		URL: prURL, Number: 43, SourceBranch: "feat/x", HeadRepo: scmTestRepo.Repo, TargetBranch: "main", HeadSHA: headSHA,
	}
	f.provider.observations[43] = failingSCMObservation(prURL, 43, headSHA, logTail)
	if err := f.observer.Poll(ctx); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	typed := f.pane.typed()
	if len(typed) != 1 {
		t.Fatalf("parked agent received %d messages, want the CI nudge typed straight in: %v", len(typed), typed)
	}
	if !strings.Contains(typed[0], logTail) {
		t.Fatalf("delivered message %q does not carry the failing check's log tail", typed[0])
	}
	held, err := f.queue.List(ctx, f.session.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("a parked agent's nudge was held instead of delivered: %v", held)
	}
}
