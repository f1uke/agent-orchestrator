package lifecycle

import (
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// An agent ending ITSELF is the one route to termination that reports nothing to
// AO first. These pin the two guards that route has to carry:
//
//  1. an ending whose work never reached anyone PARKS, so the board says "waiting"
//     instead of "finished";
//  2. an ending that really is the end runs the same crew fan-out Teardown runs,
//     so a dev cannot terminate out from under its qa.

// devWithWorktree is a crew dev exactly as the incident had it: a worker holding
// a materialized worktree, no PR, a live qa beside it.
func devWithWorktree(id domain.SessionID) domain.SessionRecord {
	r := working(id)
	r.Kind = domain.KindWorker
	r.CrewID = id
	r.CrewRole = domain.CrewRoleDev
	r.Metadata = domain.SessionMetadata{Branch: "feature/task", WorkspacePath: "/ws/feature/task"}
	return r
}

func exitSignal() ports.ActivitySignal {
	return ports.ActivitySignal{Valid: true, State: domain.ActivityExited, End: &ports.SessionEnd{Reason: "other"}}
}

// The incident. dev was told not to push, obeyed, reported, and ended itself. Its
// five commits had reached nobody, so the ending must not read as finished - and
// it must not take the crew or the worktree with it.
func TestAgentExit_UndeliveredDevParksAndLeavesItsCrewAlone(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = devWithWorktree("mer-1")
	spy := &crewReaperSpy{}
	m.SetCrewReaper(spy.fn(st))

	if err := m.ApplyActivitySignal(ctx, "mer-1", exitSignal()); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if got.IsTerminated {
		t.Fatal("a dev holding work nobody has seen was TERMINATED by its own exit; it must park")
	}
	if !got.IsSuspended || got.SleepReason != domain.SleepReasonUndelivered {
		t.Fatalf("suspended=%v reason=%q, want a parked row asleep for %q so the reaper leaves it alone",
			got.IsSuspended, got.SleepReason, domain.SleepReasonUndelivered)
	}
	if got.Activity.State != domain.ActivityParked {
		t.Fatalf("activity = %q, want %q so the board reads needs_input", got.Activity.State, domain.ActivityParked)
	}
	if got.FirstSignalAt.IsZero() {
		t.Fatal("FirstSignalAt unset: status derivation reads parked only for a session that has signalled")
	}
	if got.Termination != (domain.Termination{}) {
		t.Fatalf("termination account = %+v, want none: the session has not ended", got.Termination)
	}
	if len(spy.calls) != 0 {
		t.Fatalf("a PARKED dev must not reap its crew: %v", spy.calls)
	}
}

// A parked row is only useful if it survives the reaper: the agent's exit killed
// its tmux, so a dead-runtime probe follows within the minute.
func TestAgentExit_ParkedDevSurvivesTheDeadRuntimeProbe(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = devWithWorktree("mer-1")

	if err := m.ApplyActivitySignal(ctx, "mer-1", exitSignal()); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Probe: ports.ProbeDead}); err != nil {
		t.Fatal(err)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("the reaper terminated the parked dev; parking bought 60 seconds and nothing else")
	}
}

// The other half: an ending that IS the end still ends the whole task. dev owns
// the branch, the worktree and the PR, so its qa may not outlive it.
func TestAgentExit_DeliveredDevEndsTheWholeTask(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = devWithWorktree("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1"}}
	spy := &crewReaperSpy{}
	m.SetCrewReaper(spy.fn(st))

	if err := m.ApplyActivitySignal(ctx, "mer-1", exitSignal()); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if !got.IsTerminated {
		t.Fatal("a dev whose work is out did not terminate on its own exit")
	}
	if got.IsSuspended {
		t.Fatal("a terminated session must not also be suspended")
	}
	if len(spy.calls) != 1 || spy.calls[0] != "mer-1" {
		t.Fatalf("crew reaper calls = %v, want exactly one for mer-1: dev ending ends the task", spy.calls)
	}
	if spy.causes[0] != domain.TerminationCauseDevExited {
		t.Fatalf("cause = %q, want %q", spy.causes[0], domain.TerminationCauseDevExited)
	}
	if spy.devWasTerminal[0] {
		t.Fatal("the crew was reaped AFTER dev terminated; the order is members, then dev")
	}
	if got.Termination.Source != domain.TerminationSourceAgent || got.Termination.Reason != "other" {
		t.Fatalf("termination = %+v, want the agent's own account preserved", got.Termination)
	}
}

// Best-effort, exactly as on the merge path: a member that will not die must not
// stop dev from recording that it ended.
func TestAgentExit_CrewReaperFailureStillTerminatesDev(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = devWithWorktree("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1"}}
	m.SetCrewReaper((&crewReaperSpy{err: errors.New("tmux is wedged")}).fn(st))

	if err := m.ApplyActivitySignal(ctx, "mer-1", exitSignal()); err != nil {
		t.Fatalf("a failed crew teardown must not fail the hook: %v", err)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("a failed crew teardown left dev un-terminated")
	}
}

// A SUBORDINATE owns no branch, no worktree and no PR: it delivers its verdict
// through dev, and its ending fans out to nobody. Parking every qa that finishes
// would park every healthy crew.
func TestAgentExit_SubordinateStillTerminates(t *testing.T) {
	m, st, _ := newManager()
	qa := devWithWorktree("mer-2")
	qa.CrewID = "mer-1"
	qa.CrewRole = domain.CrewRoleQA
	st.sessions["mer-2"] = qa
	spy := &crewReaperSpy{}
	m.SetCrewReaper(spy.fn(st))

	if err := m.ApplyActivitySignal(ctx, "mer-2", exitSignal()); err != nil {
		t.Fatal(err)
	}
	if !st.sessions["mer-2"].IsTerminated {
		t.Fatal("a crew member that ended itself must still terminate")
	}
	if len(spy.calls) != 1 {
		t.Fatalf("the fan-out is a no-op for a subordinate, but it is still asked: calls = %v", spy.calls)
	}
}

// Orchestrators never own a PR and end themselves routinely. Parking them would
// leave every project's dispatcher sitting in "Needs you".
func TestAgentExit_OrchestratorStillTerminates(t *testing.T) {
	m, st, _ := newManager()
	rec := devWithWorktree("mer-o")
	rec.Kind = domain.KindOrchestrator
	rec.CrewID = ""
	rec.CrewRole = ""
	st.sessions["mer-o"] = rec

	if err := m.ApplyActivitySignal(ctx, "mer-o", exitSignal()); err != nil {
		t.Fatal(err)
	}
	if !st.sessions["mer-o"].IsTerminated {
		t.Fatal("an orchestrator that ended itself must still terminate")
	}
}

// No worktree, no work: a session that never materialized anywhere has nothing
// undelivered to hold, so it terminates as it always did.
func TestAgentExit_WorkerWithNoWorktreeStillTerminates(t *testing.T) {
	m, st, _ := newManager()
	rec := devWithWorktree("mer-3")
	rec.Metadata = domain.SessionMetadata{}
	st.sessions["mer-3"] = rec

	if err := m.ApplyActivitySignal(ctx, "mer-3", exitSignal()); err != nil {
		t.Fatal(err)
	}
	if !st.sessions["mer-3"].IsTerminated {
		t.Fatal("a session with no workspace must terminate on its own exit")
	}
}

// A solo worker is the incident without the crew: five commits, no PR, ended
// itself. The board must say "waiting", not "finished".
func TestAgentExit_UndeliveredSoloWorkerParks(t *testing.T) {
	m, st, _ := newManager()
	rec := devWithWorktree("mer-4")
	rec.CrewID = ""
	rec.CrewRole = ""
	st.sessions["mer-4"] = rec

	if err := m.ApplyActivitySignal(ctx, "mer-4", exitSignal()); err != nil {
		t.Fatal(err)
	}
	if st.sessions["mer-4"].IsTerminated {
		t.Fatal("a solo worker holding work nobody has seen was terminated by its own exit")
	}
}

// A prepared TODO has no runtime and no work; it must not park.
func TestAgentExit_TodoStillTerminates(t *testing.T) {
	m, st, _ := newManager()
	rec := devWithWorktree("mer-5")
	rec.IsTodo = true
	st.sessions["mer-5"] = rec

	if err := m.ApplyActivitySignal(ctx, "mer-5", exitSignal()); err != nil {
		t.Fatal(err)
	}
	if !st.sessions["mer-5"].IsTerminated {
		t.Fatal("a TODO row must terminate on an exit signal")
	}
}

// The park is idempotent: the harness may report the ending more than once, and
// a re-delivered exit must not flip the parked row to terminated.
func TestAgentExit_RepeatedExitDoesNotUnparkIntoTerminated(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = devWithWorktree("mer-1")

	for i := 0; i < 2; i++ {
		if err := m.ApplyActivitySignal(ctx, "mer-1", exitSignal()); err != nil {
			t.Fatal(err)
		}
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("a second exit signal terminated the parked row")
	}
}

// A session blocked on a permission prompt when its agent quit still LEFT
// waiting_input. Splitting the ending out of ApplyActivitySignal must not make an
// abandoned prompt the one dwell that never gets measured.
func TestAgentExit_FromWaitingInputStillEmitsTheDwell(t *testing.T) {
	st := newFakeStore()
	sink := &telemetrySink{}
	m := New(st, nil, WithTelemetry(sink))
	now := time.Unix(100, 0).UTC()
	m.clock = func() time.Time { return now }
	rec := devWithWorktree("mer-1")
	rec.Activity = domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: now.Add(-3 * time.Second)}
	rec.FirstSignalAt = now.Add(-time.Minute)
	st.sessions["mer-1"] = rec

	if err := m.ApplyActivitySignal(ctx, "mer-1", exitSignal()); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 || sink.events[0].Name != "ao.session.waiting_input_exited" {
		t.Fatalf("events = %#v, want one waiting_input_exited", sink.events)
	}
	if got := sink.events[0].Payload["dwell_ms"]; got != int64(3000) {
		t.Fatalf("dwell_ms = %#v, want 3000", got)
	}
}
