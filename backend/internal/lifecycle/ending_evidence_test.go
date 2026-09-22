package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// THE EVIDENCE AN ENDING LEAVES.
//
// On 2026-09-22 three sessions ended within 105 milliseconds of each other and
// the record said `agent`/`other` for all three - Claude Code's catch-all - so
// the cause could not be established. These pin the two halves of the fix:
// every route to termination now hands the side journal what AO knew at that
// instant, and every route reaps the panes the session started.

type endingSpy struct {
	mu   sync.Mutex
	seen []ports.SessionEnding
}

func (s *endingSpy) RecordEnding(_ context.Context, e ports.SessionEnding) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, e)
}

func (s *endingSpy) only(t *testing.T) ports.SessionEnding {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seen) != 1 {
		t.Fatalf("recorded %d endings, want exactly 1", len(s.seen))
	}
	return s.seen[0]
}

func (s *endingSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}

func managerWithSpy() (*Manager, *fakeStore, *endingSpy) {
	st := newFakeStore()
	spy := &endingSpy{}
	return New(st, &fakeMessenger{}, WithEndingSink(spy)), st, spy
}

// The incident's own route: an agent that ended itself.
func TestEnding_AgentExitIsRecordedWithWhatAOKnew(t *testing.T) {
	m, st, spy := managerWithSpy()
	rec := working("mer-1")
	rec.Kind = domain.KindWorker
	rec.Harness = domain.HarnessClaudeCode
	rec.FirstSignalAt = time.Now().Add(-time.Hour)
	// Silent for an hour before it stopped: the discriminator the row cannot
	// give, because the terminal write overwrites LastActivityAt.
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now().Add(-time.Hour)}
	// No workspace: a worker holding a materialized worktree with no PR PARKS
	// instead of ending (exitLeavesWorkUndelivered), which is its own test below.
	rec.Metadata = domain.SessionMetadata{
		RuntimeHandleID: "ao-feature-task",
		AgentSessionID:  "0199a0bb-1234-7000-8000-abcdefabcdef",
	}
	st.sessions["mer-1"] = rec

	sig := ports.ActivitySignal{Valid: true, State: domain.ActivityExited, End: &ports.SessionEnd{Reason: "other"}}
	if err := m.ApplyActivitySignal(ctx, "mer-1", sig); err != nil {
		t.Fatal(err)
	}

	got := spy.only(t)
	if got.Source != domain.TerminationSourceAgent || got.Reason != "other" {
		t.Errorf("source/reason = %q/%q, want the agent's own account", got.Source, got.Reason)
	}
	if got.LastState != domain.ActivityIdle {
		t.Errorf("lastState = %q, want what it was doing BEFORE the terminal write", got.LastState)
	}
	if got.LastActivityAt != rec.Activity.LastActivityAt {
		t.Errorf("lastActivityAt = %v, want the pre-write value (%v) - the gap to the ending is the evidence",
			got.LastActivityAt, rec.Activity.LastActivityAt)
	}
	if got.RuntimeHandleID != "ao-feature-task" {
		t.Errorf("runtimeHandleId = %q, want the pane to probe", got.RuntimeHandleID)
	}
	if got.AgentSessionID != rec.Metadata.AgentSessionID {
		t.Errorf("agentSessionId = %q, want the address of the transcript", got.AgentSessionID)
	}
	if got.Harness != domain.HarnessClaudeCode {
		t.Errorf("harness = %q, want it recorded - a cluster confined to one harness says something", got.Harness)
	}
	if got.At.IsZero() {
		t.Error("the ending must be stamped; the timing is half the evidence")
	}
}

// An AO-ordered teardown is recorded too, and names the operation. The journal
// has to hold BOTH so a reader can tell an ordered ending from an unordered one
// without cross-reading the database.
func TestEnding_AOTeardownNamesTheOperation(t *testing.T) {
	m, st, spy := managerWithSpy()
	st.sessions["mer-1"] = working("mer-1")

	if err := m.MarkTerminated(ctx, "mer-1", domain.TerminationCauseDaemonShutdown); err != nil {
		t.Fatal(err)
	}
	got := spy.only(t)
	if got.Source != domain.TerminationSourceAO || got.Reason != domain.TerminationCauseDaemonShutdown {
		t.Fatalf("source/reason = %q/%q, want the AO operation that ordered it", got.Source, got.Reason)
	}
}

// Nobody reported this one; the reaper inferred it. Recording it as inference is
// what keeps it from being read as a report.
func TestEnding_RuntimeGoneIsRecordedAsInference(t *testing.T) {
	m, st, spy := managerWithSpy()
	rec := working("mer-1")
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now().Add(-100 * time.Hour)}
	st.sessions["mer-1"] = rec

	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Probe: ports.ProbeDead}); err != nil {
		t.Fatal(err)
	}
	got := spy.only(t)
	if got.Source != domain.TerminationSourceRuntimeGone {
		t.Fatalf("source = %q, want %q", got.Source, domain.TerminationSourceRuntimeGone)
	}
}

// A write that changed nothing is not an ending. Recording one would put a
// session in the journal twice and invent a second event out of a retry.
func TestEnding_AnAlreadyTerminatedRowRecordsNothing(t *testing.T) {
	m, st, spy := managerWithSpy()
	rec := working("mer-1")
	rec.IsTerminated = true
	st.sessions["mer-1"] = rec

	if err := m.MarkTerminated(ctx, "mer-1", domain.TerminationCauseKill); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Probe: ports.ProbeDead}); err != nil {
		t.Fatal(err)
	}
	if n := spy.count(); n != 0 {
		t.Fatalf("recorded %d endings for a row that had already ended, want 0", n)
	}
}

// A dev holding work nobody has seen PARKS rather than ending - but its agent
// still STOPPED, and on 2026-09-22 each mass ending hid one session this way
// (nter-ios-app-47 at 06:37:17.481, nter-ios-app-79 at 15:06:43.807). So the
// journal gets the stop, marked parked, while the row records no termination
// and the panes stay up for the resume.
func TestEnding_ParkedExitIsJournalledButNotEnded(t *testing.T) {
	m, st, spy := managerWithSpy()
	rec := devWithWorktree("mer-1")
	rec.Harness = domain.HarnessClaudeCode
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now().Add(-6 * time.Hour)}
	rec.Metadata.RuntimeHandleID = "ao-feature-task"
	st.sessions["mer-1"] = rec
	reaped := 0
	m.SetSessionPaneReaper(func(context.Context, domain.SessionID) error { reaped++; return nil })

	if err := m.ApplyActivitySignal(ctx, "mer-1", exitSignal()); err != nil {
		t.Fatal(err)
	}
	got := spy.only(t)
	if got.Outcome != ports.EndingParked {
		t.Errorf("outcome = %q, want parked", got.Outcome)
	}
	if got.Source != domain.TerminationSourceAgent || got.Reason != "other" {
		t.Errorf("source/reason = %q/%q, want the agent's own account", got.Source, got.Reason)
	}
	if got.LastState != domain.ActivityIdle || got.LastActivityAt != rec.Activity.LastActivityAt {
		t.Errorf("lastState/lastActivityAt = %q/%v, want the pre-write values", got.LastState, got.LastActivityAt)
	}
	if got.RuntimeHandleID != "ao-feature-task" {
		t.Errorf("runtimeHandleId = %q, want the pane to probe", got.RuntimeHandleID)
	}
	row := st.sessions["mer-1"]
	if row.IsTerminated || row.Termination.Source != "" {
		t.Errorf("the row recorded a termination (%+v); a parked session has not ended", row.Termination)
	}
	if reaped != 0 {
		t.Error("a parked session's panes were reaped; it is still on the board")
	}

	// A second exit report against the already-parked row is a no-op, and must
	// not journal the same stop twice.
	if err := m.ApplyActivitySignal(ctx, "mer-1", exitSignal()); err != nil {
		t.Fatal(err)
	}
	if n := spy.count(); n != 1 {
		t.Errorf("recorded %d stops for one parked exit, want 1", n)
	}
}

// Every terminal write says so, so a reader never has to infer it from absence.
func TestEnding_TerminationIsMarkedTerminated(t *testing.T) {
	m, st, spy := managerWithSpy()
	st.sessions["mer-1"] = working("mer-1")
	if err := m.MarkTerminated(ctx, "mer-1", domain.TerminationCauseKill); err != nil {
		t.Fatal(err)
	}
	if got := spy.only(t).Outcome; got != ports.EndingTerminated {
		t.Errorf("outcome = %q, want terminated", got)
	}
}

// A Manager with no sink behaves exactly as it did before - the journal is an
// addition, never a dependency.
func TestEnding_NoSinkIsHarmless(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	if err := m.MarkTerminated(ctx, "mer-1", domain.TerminationCauseKill); err != nil {
		t.Fatal(err)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("the termination itself must still land with no sink wired")
	}
}

// --- the panes an ending leaves behind --------------------------------------

// `iosrun-advisor-ios-app-13` outlived its session because an agent ending
// itself never reaches Teardown, which is where panes are reaped.
func TestEnding_AgentExitReapsTheSessionsPanes(t *testing.T) {
	m, st, _ := managerWithSpy()
	rec := working("mer-1")
	rec.Kind = domain.KindWorker
	rec.FirstSignalAt = time.Now().Add(-time.Hour)
	st.sessions["mer-1"] = rec

	var reaped []domain.SessionID
	m.SetSessionPaneReaper(func(_ context.Context, id domain.SessionID) error {
		reaped = append(reaped, id)
		return nil
	})
	if err := m.ApplyActivitySignal(ctx, "mer-1", exitSignal()); err != nil {
		t.Fatal(err)
	}
	if len(reaped) != 1 || reaped[0] != "mer-1" {
		t.Fatalf("reaped %v, want the ending session's panes closed exactly once", reaped)
	}
}

// The other route that never reaches Teardown: the reaper finding the runtime
// gone. The agent pane is already dead there, but the run pane is a SEPARATE
// tmux session and survives it.
func TestEnding_RuntimeGoneReapsTheSessionsPanes(t *testing.T) {
	m, st, _ := managerWithSpy()
	rec := working("mer-1")
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now().Add(-100 * time.Hour)}
	st.sessions["mer-1"] = rec

	reaped := 0
	m.SetSessionPaneReaper(func(context.Context, domain.SessionID) error { reaped++; return nil })
	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Probe: ports.ProbeDead}); err != nil {
		t.Fatal(err)
	}
	if reaped != 1 {
		t.Fatalf("reaped %d times, want 1", reaped)
	}
}

// A pane that will not die must not turn a recorded termination into a failed
// one: the session has already ended.
func TestEnding_AFailedReapDoesNotFailTheTermination(t *testing.T) {
	m, st, _ := managerWithSpy()
	st.sessions["mer-1"] = working("mer-1")
	m.SetSessionPaneReaper(func(context.Context, domain.SessionID) error { return errors.New("tmux unreachable") })

	if err := m.MarkTerminated(ctx, "mer-1", domain.TerminationCauseKill); err != nil {
		t.Fatalf("MarkTerminated = %v, want nil - a stuck pane is not a failed termination", err)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("the row must still record that the session ended")
	}
}
