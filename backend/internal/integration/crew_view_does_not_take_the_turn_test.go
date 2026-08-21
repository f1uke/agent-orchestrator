package integration

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LOOKING AT A CREW MEMBER MUST NOT TAKE ITS TURN.
//
// The incident: dev's PR merged, so lifecycle terminated it; twelve seconds
// later qa - asleep because it was not its turn - was running, and nobody had
// pressed anything. The desktop app POSTs /api/v1/sessions/{id}/wake whenever a
// session view opens or a session is placed in a split pane, and that endpoint is
// Service.Wake, which resumed qa because the slot was free.
//
// These tests drive the REAL manager, lifecycle reducer, SQLite store and git
// worktree, and they call the same service method the endpoint calls.

// setupCrew spawns a standard task, which forms dev + a born-suspended qa.
func setupCrew(t *testing.T, s *crewStack) (dev, qa domain.SessionRecord) {
	t.Helper()
	ctx := context.Background()
	devRec, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/task", Prompt: "build it",
		TaskSize: domain.TaskSizeStandard,
	})
	if err != nil {
		t.Fatalf("spawn dev: %v", err)
	}
	qaRec, ok, err := s.qaOf(ctx, devRec.ID)
	if err != nil || !ok {
		t.Fatalf("the standard spawn formed no crew: %v", err)
	}
	if !qaRec.IsSuspended {
		t.Fatal("qa was not born suspended")
	}
	return devRec, qaRec
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

// TestCrewView_OpeningQAAfterDevEndsDoesNotWakeIt is the incident itself: dev's
// PR merged and lifecycle terminated it, so the crew slot is FREE. Opening qa's
// view must still leave qa asleep - its turn has not come, and nobody said it
// had.
func TestCrewView_OpeningQAAfterDevEndsDoesNotWakeIt(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := setupCrew(t, s)

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
	if s.rt.created != 1 {
		t.Fatalf("runtimes created = %d, want 1 (dev's): viewing qa launched an agent", s.rt.created)
	}
}

// TestCrewView_OpeningASleepingMemberWhileTheOtherWorks covers the same defect
// from the other side: dev holds the slot. Opening qa must be a quiet no-op -
// not a refusal the user has to read, and certainly not a handover.
func TestCrewView_OpeningASleepingMemberWhileTheOtherWorks(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := setupCrew(t, s)

	if _, err := s.svc.Wake(ctx, qa.ID); err != nil {
		t.Fatalf("opening a sleeping crew member's view must not error: %v", err)
	}
	if got := s.record(t, qa.ID); got.Awake() {
		t.Fatal("VIEWING qa woke it while dev was working: two agents in one worktree")
	}
	s.assertOneAwake(t, dev.ID, dev.ID, qa.ID)
}

// TestCrewView_ASleepingMemberInASplitPaneStaysAsleep is the second way the
// renderer wakes a session: every pane of a split view is woken once, through the
// SAME endpoint. It is a distinct code path in the UI and a distinct way for a
// human to end up looking at qa, so it is asserted rather than assumed - and the
// rule lives in the daemon precisely so both paths get it from one place.
func TestCrewView_ASleepingMemberInASplitPaneStaysAsleep(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := setupCrew(t, s)
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
		t.Fatal("qa woke because it was put in a split pane")
	}
	if got := s.record(t, dev.ID); !got.IsTerminated {
		t.Fatal("the pane wake resurrected a terminated session")
	}
}

// TestCrewView_AnExplicitWakeStillTakesTheTurn is the other half of the rule. An
// ACTION may take the turn - that is what the baton bar's button and
// `ao crew wake` are for - and only a glance may not.
func TestCrewView_AnExplicitWakeStillTakesTheTurn(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := setupCrew(t, s)

	// dev is awake and holds the slot, so this is the full handover.
	if _, err := s.svc.WakeCrewMember(ctx, qa.ID); err != nil {
		t.Fatalf("explicit wake: %v", err)
	}
	s.assertOneAwake(t, qa.ID, dev.ID, qa.ID)

	// And dev, stood down for the handover, is now the one that a view may not
	// bring back: the reason travelled with the release.
	devRec := s.record(t, dev.ID)
	if !devRec.AsleepForTurn() {
		t.Fatalf("dev was stood down without recording why: suspended=%v reason=%q", devRec.IsSuspended, devRec.SleepReason)
	}
	if _, err := s.svc.Wake(ctx, dev.ID); err != nil {
		t.Fatalf("viewing dev: %v", err)
	}
	s.assertOneAwake(t, qa.ID, dev.ID, qa.ID)
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
	dev, qa := setupCrew(t, s)

	// A fresh spawn was never asleep, so there is no wake to attribute.
	if got := s.record(t, dev.ID); got.WokenBy != "" {
		t.Fatalf("a fresh spawn recorded wokenBy=%q, want empty", got.WokenBy)
	}
	if got := s.record(t, qa.ID); got.SleepReason != domain.SleepReasonTurn {
		t.Fatalf("a born-suspended qa recorded sleepReason=%q, want %q", got.SleepReason, domain.SleepReasonTurn)
	}

	if _, err := s.svc.WakeCrewMember(ctx, qa.ID); err != nil {
		t.Fatalf("explicit wake: %v", err)
	}
	if got := s.record(t, qa.ID); got.WokenBy != domain.WokenByWake {
		t.Fatalf("an explicit wake recorded wokenBy=%q, want %q", got.WokenBy, domain.WokenByWake)
	}
	// The member it displaced is asleep again, and its own attribution is gone:
	// the two fields describe opposite halves of one transition, so at most one of
	// them is ever set.
	devRec := s.record(t, dev.ID)
	if devRec.SleepReason != domain.SleepReasonTurn || devRec.WokenBy != "" {
		t.Fatalf("the displaced member recorded reason=%q wokenBy=%q, want %q and empty", devRec.SleepReason, devRec.WokenBy, domain.SleepReasonTurn)
	}
}
