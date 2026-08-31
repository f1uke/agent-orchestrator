package sessionmanager

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The worktree directory and the tmux session name are both derived from
// project+branch (tmux.SessionNameFor), never from the session id. So every
// attempt at one task on one branch addresses ONE pane and ONE directory, and a
// finished attempt's teardown aims at both while a later attempt is living in
// them. seedCoTenants builds exactly that: a terminated dev from an earlier
// crew, and a live dev from the current one, on the same branch.
const sharedBranchPath = "/ws/feature/retried-task"
const sharedBranchHandle = "mer-feature-retried-task"

func seedCoTenants(st *fakeStore) (dead, live domain.SessionRecord) {
	meta := domain.SessionMetadata{
		Branch:          "feature/retried-task",
		WorkspacePath:   sharedBranchPath,
		RuntimeHandleID: sharedBranchHandle,
	}
	dead = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		CrewID: "mer-1", CrewRole: domain.CrewRoleDev,
		Metadata: meta, IsTerminated: true,
		Activity: domain.Activity{State: domain.ActivityExited},
	}
	live = domain.SessionRecord{
		ID: "mer-9", ProjectID: "mer", Kind: domain.KindWorker,
		CrewID: "mer-9", CrewRole: domain.CrewRoleDev,
		Metadata: meta,
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.sessions[dead.ID] = dead
	st.sessions[live.ID] = live
	return dead, live
}

// TestTeardown_LeavesALiveCoTenantsPaneAndTreeStanding is the incident: the
// auto-reclaim loop tears down a long-finished session, and because the handle
// and the path are named after the BRANCH, `tmux kill-session` lands on the pane
// a DIFFERENT, live session is working in and `rm -rf` lands on the tree under
// it. Observed four times on one branch (see the diagnosis in the AO knowledge
// store); the live agent's harness then fires SessionEnd on the way down, so the
// record blamed the agent for a kill AO performed.
func TestTeardown_LeavesALiveCoTenantsPaneAndTreeStanding(t *testing.T) {
	m, st, rt, ws := newManager()
	dead, live := seedCoTenants(st)

	res, err := m.Teardown(ctx, dead.ID, domain.TerminationCauseAutoReclaim)
	if err != nil {
		t.Fatalf("Teardown(finished): %v", err)
	}
	if len(rt.destroyedIDs) != 0 {
		t.Fatalf("runtimes destroyed = %v; that pane is a live session's", rt.destroyedIDs)
	}
	if ws.destroyed != 0 {
		t.Fatalf("workspace destroyed %d time(s); a live session is standing in it", ws.destroyed)
	}
	if res.Freed {
		t.Fatal("reported the disk freed while a live session still occupies it")
	}
	if res.Reason != ReasonWorkspaceShared {
		t.Fatalf("reason = %q, want %q so the reclaim loop RETRIES rather than recording a success", res.Reason, ReasonWorkspaceShared)
	}
	if !st.sessions[dead.ID].IsTerminated {
		t.Fatal("the finished session must still be marked terminated; only its shared resources are spared")
	}
	if st.sessions[live.ID].IsTerminated {
		t.Fatal("the live co-tenant was terminated by someone else's teardown")
	}
}

// TestTeardown_TerminatedCoTenantDoesNotHoldTheTree: the guard is about LIVE
// tenants only. Two finished attempts on one branch must still free the disk,
// or a retried task would strand its worktree for ever.
func TestTeardown_TerminatedCoTenantDoesNotHoldTheTree(t *testing.T) {
	m, st, rt, ws := newManager()
	dead, live := seedCoTenants(st)
	live.IsTerminated = true
	live.Activity = domain.Activity{State: domain.ActivityExited}
	st.sessions[live.ID] = live

	res, err := m.Teardown(ctx, dead.ID, domain.TerminationCauseAutoReclaim)
	if err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if !res.Freed {
		t.Fatalf("two finished sessions on one branch must free the disk; reason = %q", res.Reason)
	}
	if ws.destroyed != 1 {
		t.Fatalf("workspace destroyed %d time(s), want 1", ws.destroyed)
	}
	if len(rt.destroyedIDs) != 1 || rt.destroyedIDs[0] != sharedBranchHandle {
		t.Fatalf("runtimes destroyed = %v, want [%s]", rt.destroyedIDs, sharedBranchHandle)
	}
}

// TestTeardown_SuspendedCoTenantHoldsTheTree: a suspended session is PAUSED, not
// finished, and that tree is exactly what it resumes into. This is the #273 park
// (`sleepReason=undelivered`) seen from the other side: parking the victim is
// worthless if the next sweep deletes the tree it was parked to protect.
func TestTeardown_SuspendedCoTenantHoldsTheTree(t *testing.T) {
	m, st, rt, ws := newManager()
	dead, live := seedCoTenants(st)
	live.IsSuspended = true
	live.Activity = domain.Activity{State: domain.ActivityParked}
	live.SleepReason = domain.SleepReasonUndelivered
	st.sessions[live.ID] = live

	res, err := m.Teardown(ctx, dead.ID, domain.TerminationCauseAutoReclaim)
	if err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if res.Freed || ws.destroyed != 0 || len(rt.destroyedIDs) != 0 {
		t.Fatalf("a parked session's tree/pane was destroyed: freed=%v ws=%d rt=%v", res.Freed, ws.destroyed, rt.destroyedIDs)
	}
}

// TestTeardown_SoloSessionUnaffected pins the no-regression edge: a session that
// shares its handle and path with nobody tears down byte-for-byte as before.
func TestTeardown_SoloSessionUnaffected(t *testing.T) {
	m, st, rt, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{
		Branch: "feature/solo", WorkspacePath: "/ws/feature/solo", RuntimeHandleID: "h-solo",
	})

	res, err := m.Teardown(ctx, "mer-1", domain.TerminationCauseAutoReclaim)
	if err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if !res.Freed || ws.destroyed != 1 {
		t.Fatalf("solo teardown changed: freed=%v reason=%q destroyed=%d", res.Freed, res.Reason, ws.destroyed)
	}
	if len(rt.destroyedIDs) != 1 || rt.destroyedIDs[0] != "h-solo" {
		t.Fatalf("runtimes destroyed = %v, want [h-solo]", rt.destroyedIDs)
	}
}
