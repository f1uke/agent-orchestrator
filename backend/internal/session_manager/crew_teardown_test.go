package sessionmanager

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const crewPath = "/ws/feature/task"

// seedCrew puts a dev + one qa member on one worktree, both live.
func seedCrew(st *fakeStore) (dev, qa domain.SessionRecord) {
	dev = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		CrewID: "mer-1", CrewRole: domain.CrewRoleDev,
		Metadata: domain.SessionMetadata{Branch: "feature/task", WorkspacePath: crewPath, RuntimeHandleID: "h-dev", Prompt: "build it"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	qa = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker,
		CrewID: "mer-1", CrewRole: domain.CrewRoleQA,
		Metadata: domain.SessionMetadata{Branch: "feature/task", WorkspacePath: crewPath, RuntimeHandleID: "h-qa", Prompt: "test it"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.sessions[dev.ID] = dev
	st.sessions[qa.ID] = qa
	return dev, qa
}

// TestTeardown_SubordinateLeavesDevsWorkspaceStanding is the local case:
// terminating qa ends qa and nothing else. dev is still running in that tree, so
// the tree must survive — and the refusal has to be legible, because a caller
// that reads Freed=false as "reclaim it later" is the only thing standing between
// a shared worktree and a disk leak.
func TestTeardown_SubordinateLeavesDevsWorkspaceStanding(t *testing.T) {
	m, st, rt, ws := newManager()
	dev, qa := seedCrew(st)

	res, err := m.Teardown(ctx, qa.ID, domain.TerminationCauseKill)
	if err != nil {
		t.Fatalf("Teardown(qa): %v", err)
	}
	if res.Freed {
		t.Fatal("terminating a crew member freed the worktree its dev is still working in")
	}
	if res.Reason != ReasonWorkspaceShared {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonWorkspaceShared)
	}
	if ws.destroyed != 0 {
		t.Fatalf("workspace destroyed %d time(s); a live dev is in it", ws.destroyed)
	}
	if !st.sessions[qa.ID].IsTerminated {
		t.Fatal("the crew member must still be terminated; only its worktree is spared")
	}
	if st.sessions[dev.ID].IsTerminated {
		t.Fatal("terminating a subordinate must not terminate dev")
	}
	if got := st.sessions[dev.ID].Metadata.WorkspacePath; got != crewPath {
		t.Fatalf("dev workspace = %q, want it untouched at %q", got, crewPath)
	}
	if len(rt.destroyedIDs) != 1 || rt.destroyedIDs[0] != "h-qa" {
		t.Fatalf("runtimes destroyed = %v, want only the member's own pane [h-qa]", rt.destroyedIDs)
	}
}

// TestTeardown_DevTearsDownTheWholeTask: there is no "terminate dev alone". dev
// owns the branch, the worktree and the PR, so a subordinate left running after
// it would be an agent working on something nobody will land, in a tree about to
// be removed. Subordinates go first, then the tree is freed in the same pass.
func TestTeardown_DevTearsDownTheWholeTask(t *testing.T) {
	m, st, rt, ws := newManager()
	dev, qa := seedCrew(st)

	res, err := m.Teardown(ctx, dev.ID, domain.TerminationCauseKill)
	if err != nil {
		t.Fatalf("Teardown(dev): %v", err)
	}
	if !res.Freed {
		t.Fatalf("tearing the task down left the worktree on disk (reason %q)", res.Reason)
	}
	if !st.sessions[qa.ID].IsTerminated {
		t.Fatal("dev's teardown left its crew member running on a worktree that is now gone")
	}
	if !st.sessions[dev.ID].IsTerminated {
		t.Fatal("dev was not terminated")
	}
	if ws.destroyed != 1 {
		t.Fatalf("workspace destroyed %d time(s), want exactly 1", ws.destroyed)
	}
	// qa's pane before dev's: the subordinate must not outlive the owner even for
	// the length of a teardown.
	if len(rt.destroyedIDs) != 2 || rt.destroyedIDs[0] != "h-qa" || rt.destroyedIDs[1] != "h-dev" {
		t.Fatalf("runtime destroy order = %v, want [h-qa h-dev]", rt.destroyedIDs)
	}
}

// TestTeardown_LastMemberStandingFreesTheTree covers the reclaim ordering the
// loop actually produces: qa is torn down first (its tree is kept for dev), dev
// finishes later, and the disk is freed then. A worktree that could only ever be
// freed by one specific member would be a leak whenever that member went first.
func TestTeardown_LastMemberStandingFreesTheTree(t *testing.T) {
	m, st, _, ws := newManager()
	dev, qa := seedCrew(st)

	if res, err := m.Teardown(ctx, qa.ID, domain.TerminationCauseKill); err != nil || res.Freed {
		t.Fatalf("Teardown(qa) = %+v, %v; want a refusal", res, err)
	}
	res, err := m.Teardown(ctx, dev.ID, domain.TerminationCauseKill)
	if err != nil {
		t.Fatalf("Teardown(dev): %v", err)
	}
	if !res.Freed {
		t.Fatalf("the last member standing did not free the worktree (reason %q)", res.Reason)
	}
	if ws.destroyed != 1 {
		t.Fatalf("workspace destroyed %d time(s), want exactly 1", ws.destroyed)
	}
}

// TestTeardown_SuspendedMemberStillHoldsTheTree. A suspended session is PAUSED,
// not finished: its worktree is exactly what it resumes into. Reading "no tmux"
// as "done with the disk" would delete a task the user only walked away from.
func TestTeardown_SuspendedMemberStillHoldsTheTree(t *testing.T) {
	m, st, _, ws := newManager()
	dev, qa := seedCrew(st)
	rec := st.sessions[qa.ID]
	rec.IsSuspended = true
	rec.Metadata.RuntimeHandleID = ""
	st.sessions[qa.ID] = rec

	// Tear down dev WITHOUT the fan-out by pretending it is the subordinate: the
	// point here is only that a suspended holder counts.
	devRec := st.sessions[dev.ID]
	devRec.CrewRole = domain.CrewRoleQA
	st.sessions[dev.ID] = devRec

	res, err := m.Teardown(ctx, dev.ID, domain.TerminationCauseKill)
	if err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if res.Freed || res.Reason != ReasonWorkspaceShared {
		t.Fatalf("a suspended crewmate did not hold the worktree: freed=%v reason=%q", res.Freed, res.Reason)
	}
	if ws.destroyed != 0 {
		t.Fatal("the worktree a suspended member resumes into was destroyed")
	}
}

// TestTeardown_SoloIsUnchanged is the preservation guard. A solo session has no
// crew fields, so neither the fan-out nor the refcount may fire: it frees its own
// worktree, exactly as it does today.
func TestTeardown_SoloIsUnchanged(t *testing.T) {
	m, st, rt, ws := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{Branch: "feature/solo", WorkspacePath: "/ws/feature/solo", RuntimeHandleID: "h1"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	// A second, unrelated solo session on the SAME path (which is what every
	// orchestrator of a project genuinely looks like) must not hold it either:
	// the refcount is crew-scoped precisely so this case is untouched.
	st.sessions["mer-2"] = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{Branch: "feature/solo", WorkspacePath: "/ws/feature/solo", RuntimeHandleID: "h2"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	res, err := m.Teardown(ctx, "mer-1", domain.TerminationCauseKill)
	if err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if !res.Freed || res.Reason != "" {
		t.Fatalf("solo teardown = freed %v reason %q, want freed with no reason", res.Freed, res.Reason)
	}
	if ws.destroyed != 1 {
		t.Fatalf("workspace destroyed %d time(s), want 1", ws.destroyed)
	}
	if len(rt.destroyedIDs) != 1 || rt.destroyedIDs[0] != "h1" {
		t.Fatalf("runtimes destroyed = %v, want only [h1]", rt.destroyedIDs)
	}
	if st.sessions["mer-2"].IsTerminated {
		t.Fatal("a solo teardown terminated an unrelated session sharing its path")
	}
}

// TestSaveAndTeardownAll_CrewCapturesOnceAndKeepsTheTreeForDev pins the shutdown
// path. Both members are going down, but only ONE of them may stash and remove
// the shared tree, and it has to be dev — the preserve ref is filed under the
// session that owns the work.
func TestSaveAndTeardownAll_CrewCapturesOnceAndKeepsTheTreeForDev(t *testing.T) {
	m, st, _, ws := newManager()
	dev, qa := seedCrew(st)
	ws.stashRef = "refs/ao/preserved/mer-1"

	if err := m.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("SaveAndTeardownAll: %v", err)
	}
	if ws.stashCalls != 1 {
		t.Fatalf("StashUncommitted called %d time(s), want exactly 1 for a shared worktree", ws.stashCalls)
	}
	devRows := st.worktrees[dev.ID]
	if len(devRows) != 1 || devRows[0].PreservedRef != ws.stashRef {
		t.Fatalf("dev restore marker = %+v, want the preserve ref %q", devRows, ws.stashRef)
	}
	qaRows := st.worktrees[qa.ID]
	if len(qaRows) != 1 {
		t.Fatalf("crew member restore markers = %d, want 1 so RestoreAll brings it back", len(qaRows))
	}
	if qaRows[0].PreservedRef != "" {
		t.Fatalf("crew member preserve ref = %q, want empty: it removed nothing, so it preserved nothing", qaRows[0].PreservedRef)
	}
	if !st.sessions[dev.ID].IsTerminated || !st.sessions[qa.ID].IsTerminated {
		t.Fatal("shutdown must terminate both crew members")
	}
}

// TestReconcileLive_CrewMemberDoesNotDeleteALiveDevsWorktree is the bug this
// guard exists for. On boot, a member whose tmux died with the daemon is not
// idle, so it takes the capture-and-ForceDestroy path — which would remove the
// worktree a still-adopted dev is working in, and the task could not continue.
func TestReconcileLive_CrewMemberDoesNotDeleteALiveDevsWorktree(t *testing.T) {
	m, st, rt, ws := newManager()
	dev, _ := seedCrew(st)
	// dev's tmux survived the restart; qa's did not.
	rt.aliveByHandle = map[string]bool{"h-dev": true}

	if err := m.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	for _, call := range ws.calls {
		if strings.HasPrefix(call, "ForceDestroy:") {
			t.Fatalf("boot force-destroyed the shared worktree (%s) while dev was still live in it; workspace calls: %v", call, ws.calls)
		}
	}
	if ws.stashCalls != 0 {
		t.Fatalf("boot stashed a live dev's uncommitted work (%d call(s)) on the crew member's behalf", ws.stashCalls)
	}
	if st.sessions[dev.ID].IsTerminated {
		t.Fatal("dev was adopted alive and must not be terminated")
	}
	if got := st.sessions[dev.ID].Metadata.WorkspacePath; got != crewPath {
		t.Fatalf("dev workspace = %q, want %q", got, crewPath)
	}
}
