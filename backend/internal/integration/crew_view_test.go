package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LOOKING AT A CREW MEMBER MUST NOT START AN AGENT NOBODY ASKED FOR.
//
// The incident: dev's PR merged, so lifecycle terminated it; twelve seconds
// later qa - which had never run - was running, and nobody had pressed anything.
// The desktop app POSTs /api/v1/sessions/{id}/wake whenever a session view opens
// or a session is placed in a split pane, and that endpoint is Service.Wake.
//
// The TURN part of that rule is gone: both members run at once, so bringing one
// up takes nothing from the other and a card no longer has to refuse to wake. The
// part that survives is the part that was never about turns - starting an agent
// for the FIRST time spends money and is a decision, while resuming one that was
// paused is what a glance has always done.
//
// These tests drive the REAL manager, lifecycle reducer, SQLite store and git
// worktree, and they call the same service method the endpoint calls.

// setupCrew spawns a standard task and makes it gain its qa the way a real one
// does: dev touches the app's runtime. The member it produces is AWAKE, which is
// what the trigger creates.
func setupCrew(t *testing.T, s *crewStack) (dev, qa domain.SessionRecord) {
	t.Helper()
	return setupCrewWithStartFailure(t, s, nil)
}

// setupCrewNeverStarted produces the one state a crew member can still be in
// without ever having run: the trigger created it and its LAUNCH failed. Starting
// is best effort - the member is on the task, visible and openable - and this is
// the state the glance rule below is about.
func setupCrewNeverStarted(t *testing.T, s *crewStack) (dev, qa domain.SessionRecord) {
	t.Helper()
	return setupCrewWithStartFailure(t, s, errors.New("stub: no tmux for the new member"))
}

func setupCrewWithStartFailure(t *testing.T, s *crewStack, startErr error) (dev, qa domain.SessionRecord) {
	t.Helper()
	ctx := context.Background()
	devRec, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/task", Prompt: "build it",
		TaskSize: domain.TaskSizeStandard,
	})
	if err != nil {
		t.Fatalf("spawn dev: %v", err)
	}
	if _, ok, _ := s.qaOf(ctx, devRec.ID); ok {
		t.Fatal("a standard spawn created a qa; it must start as dev alone")
	}
	// createErr fails exactly the next Create, which is the new member's launch.
	s.rt.createErr = startErr
	s.mgr.NoteRuntimeTouch(ctx, devRec.ID, domain.CrewJoinSim)

	qaRec, ok, err := s.qaOf(ctx, devRec.ID)
	if err != nil || !ok {
		t.Fatalf("touching the runtime created no qa: %v", err)
	}
	if startErr == nil && qaRec.IsSuspended {
		t.Fatal("qa was created asleep; there is nothing left to start it")
	}
	if startErr != nil && !qaRec.NeverStarted() {
		t.Fatalf("qa carries a runtime handle %q after a failed start", qaRec.Metadata.RuntimeHandleID)
	}
	return s.record(t, devRec.ID), qaRec
}

// qaOf finds the qa member of dev's crew.
func (s *crewStack) qaOf(ctx context.Context, dev domain.SessionID) (domain.SessionRecord, bool, error) {
	all, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return domain.SessionRecord{}, false, err
	}
	for _, rec := range all {
		if rec.CrewID == dev && rec.CrewRole == domain.CrewRoleQA {
			return rec, true, nil
		}
	}
	return domain.SessionRecord{}, false, nil
}

// TestCrewView_OpeningQAAfterDevEndsDoesNotStartIt is the incident itself: dev's
// PR merged and lifecycle terminated it. Opening qa's view must still leave qa
// asleep - it has never run, and nobody asked for it to.
func TestCrewView_OpeningQAAfterDevEndsDoesNotStartIt(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := setupCrewNeverStarted(t, s)

	// The merge reducer's own call: all PRs merged -> the worker is done.
	if err := s.lcm.MarkTerminated(ctx, dev.ID, domain.TerminationCauseWorkComplete); err != nil {
		t.Fatalf("terminate dev: %v", err)
	}

	// The exact call the renderer makes when a session view opens.
	if _, err := s.svc.Wake(ctx, qa.ID); err != nil {
		t.Fatalf("wake (view) returned an error: %v", err)
	}

	if got := s.record(t, qa.ID); got.Awake() {
		t.Fatalf("VIEWING qa woke it (suspended=%v): nobody asked for qa's turn", got.IsSuspended)
	}
	// dev's launch, and the one that failed. Viewing qa must not add a third.
	if s.rt.created != 2 {
		t.Fatalf("runtimes created = %d, want 2 (dev's, and the failed start): viewing qa launched an agent", s.rt.created)
	}
}

// TestCrewView_OpeningANeverStartedMemberWhileTheOtherWorks covers the same
// defect from the other side: dev is working. Opening qa must be a quiet no-op -
// not a refusal the user has to read, and not a second agent nobody asked for.
func TestCrewView_OpeningANeverStartedMemberWhileTheOtherWorks(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := setupCrewNeverStarted(t, s)

	if _, err := s.svc.Wake(ctx, qa.ID); err != nil {
		t.Fatalf("opening a sleeping crew member's view must not error: %v", err)
	}
	if got := s.record(t, qa.ID); got.Awake() {
		t.Fatal("VIEWING qa started it while dev was working: nobody asked for a second agent")
	}
	if got := s.awakeMembers(t, dev.ID, qa.ID); len(got) != 1 || got[0] != dev.ID {
		t.Fatalf("awake = %v, want only dev", got)
	}
}

// TestCrewView_ASleepingMemberInASplitPaneStaysAsleep is the second way the
// renderer wakes a session: every pane of a split view is woken once, through the
// SAME endpoint. It is a distinct code path in the UI and a distinct way for a
// human to end up looking at qa, so it is asserted rather than assumed - and the
// rule lives in the daemon precisely so both paths get it from one place.
func TestCrewView_ASleepingMemberInASplitPaneStaysAsleep(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := setupCrewNeverStarted(t, s)
	if err := s.lcm.MarkTerminated(ctx, dev.ID, domain.TerminationCauseWorkComplete); err != nil {
		t.Fatalf("terminate dev: %v", err)
	}

	// Placing dev and qa side by side wakes each pane's session once.
	for _, id := range []domain.SessionID{dev.ID, qa.ID} {
		if _, err := s.svc.Wake(ctx, id); err != nil {
			t.Fatalf("pane wake %s: %v", id, err)
		}
	}

	if got := s.record(t, qa.ID); got.Awake() {
		t.Fatal("qa started because it was put in a split pane")
	}
	if got := s.record(t, dev.ID); !got.IsTerminated {
		t.Fatal("the pane wake resurrected a terminated session")
	}
}

// TestCrewView_AnExplicitStartBringsItUpBesideDev is the other half of the rule.
// An ACTION starts an agent - that is what `ao crew wake` and the Start
// affordance on the card are for - and a glance does not.
func TestCrewView_AnExplicitStartBringsItUpBesideDev(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := setupCrewNeverStarted(t, s)

	if _, err := s.svc.WakeCrewMember(ctx, qa.ID); err != nil {
		t.Fatalf("explicit start: %v", err)
	}
	s.assertBothAwake(t, dev.ID, qa.ID)

	// dev is exactly where it was. Nothing was taken off it.
	devRec := s.record(t, dev.ID)
	if devRec.IsSuspended || !devRec.Awake() {
		t.Fatalf("starting qa stood dev down: suspended=%v reason=%q", devRec.IsSuspended, devRec.SleepReason)
	}
}

// TestSolo_AnIdleSweptSessionStillWakesOnOpen is the behaviour that must NOT
// change: every real session on this machine is solo, the sweep pauses it to free
// resources, and opening it brings it back. That is what the whole user-open hook
// exists for and the human relies on it daily.
func TestSolo_AnIdleSweptSessionStillWakesOnOpen(t *testing.T) {
	ctx := context.Background()
	s := newCrewStackWithIdle(t, time.Hour)
	rec, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/solo", Prompt: "work",
		// mechanical keeps this a SOLO spawn - a standard task would form a crew.
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.now = s.now.Add(2 * time.Hour)
	if err := s.mgr.CloseIdleSessions(ctx); err != nil {
		t.Fatal(err)
	}
	swept := s.record(t, rec.ID)
	if !swept.IsSuspended || swept.SleepReason != domain.SleepReasonIdle {
		t.Fatalf("the idle sweep recorded suspended=%v reason=%q, want suspended for %q", swept.IsSuspended, swept.SleepReason, domain.SleepReasonIdle)
	}

	if _, err := s.svc.Wake(ctx, rec.ID); err != nil {
		t.Fatalf("opening an idle-swept solo session: %v", err)
	}
	back := s.record(t, rec.ID)
	if !back.Awake() {
		t.Fatalf("an idle-swept solo session did NOT come back when opened: suspended=%v", back.IsSuspended)
	}
	// And the audit trail names the glance that did it.
	if back.WokenBy != domain.WokenByView {
		t.Fatalf("wokenBy = %q, want %q", back.WokenBy, domain.WokenByView)
	}
	if back.SleepReason != "" {
		t.Fatalf("sleepReason = %q, want it cleared once the session is back up", back.SleepReason)
	}
}

// TestCrewView_TheWakeIsAttributed is the audit trail. change_log recorded that
// is_suspended flipped but not WHO flipped it, so an unexplained wake could only
// be reasoned about from timestamps. Each route now names itself on the row, and
// the row is what the CDC payload carries.
func TestCrewView_TheWakeIsAttributed(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := setupCrewNeverStarted(t, s)

	// A fresh spawn was never asleep, so there is no wake to attribute.
	if got := s.record(t, dev.ID); got.WokenBy != "" {
		t.Fatalf("a fresh spawn recorded wokenBy=%q, want empty", got.WokenBy)
	}
	// A member whose start failed is asleep the way anything with no process is
	// asleep. What makes it different is that it has never RUN, which is on the
	// row already: no runtime handle.
	if got := s.record(t, qa.ID); !got.NeverStarted() {
		t.Fatalf("a born-suspended qa carries a runtime handle %q", got.Metadata.RuntimeHandleID)
	}

	if _, err := s.svc.WakeCrewMember(ctx, qa.ID); err != nil {
		t.Fatalf("explicit wake: %v", err)
	}
	if got := s.record(t, qa.ID); got.WokenBy != domain.WokenByWake {
		t.Fatalf("an explicit wake recorded wokenBy=%q, want %q", got.WokenBy, domain.WokenByWake)
	}
	// And nobody was displaced: dev is still running, with no sleep reason of its
	// own, because starting one member takes nothing from the other.
	devRec := s.record(t, dev.ID)
	if devRec.IsSuspended || devRec.SleepReason != "" {
		t.Fatalf("dev recorded suspended=%v reason=%q; starting qa must not touch it", devRec.IsSuspended, devRec.SleepReason)
	}
}
