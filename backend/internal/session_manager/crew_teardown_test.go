package sessionmanager

import (
	"errors"
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

// TestTeardown_SuspendedSubordinateGoesWithTheTask. A suspended member is
// PAUSED, not finished: its worktree is exactly what it resumes into. But when
// the TASK ends, so does it — otherwise the card would sit on the board forever
// promising a resume into a tree that has been reclaimed.
func TestTeardown_SuspendedSubordinateGoesWithTheTask(t *testing.T) {
	m, st, _, ws := newManager()
	dev, qa := seedCrew(st)
	rec := st.sessions[qa.ID]
	rec.IsSuspended = true
	rec.Metadata.RuntimeHandleID = ""
	st.sessions[qa.ID] = rec

	res, err := m.Teardown(ctx, dev.ID, domain.TerminationCauseKill)
	if err != nil {
		t.Fatalf("Teardown(dev): %v", err)
	}
	if !res.Freed || ws.destroyed != 1 {
		t.Fatalf("task teardown = freed %v (reason %q), destroys %d; want freed exactly once", res.Freed, res.Reason, ws.destroyed)
	}
	if !st.sessions[qa.ID].IsTerminated {
		t.Fatal("a suspended crew member was left on the board pointing at a reclaimed worktree")
	}
}

// TestTeardown_FanOutFailureKeepsTheTree. The fan-out is best-effort: a member
// that will not die must not stop dev from terminating, because a task nobody can
// finish is worse than a worktree that survives. But dev must then REFUSE to
// remove the tree — the surviving member is still in it — and say why, so the
// reclaim log carries the branch and the situation is recoverable.
func TestTeardown_FanOutFailureKeepsTheTree(t *testing.T) {
	m, st, rt, ws := newManager()
	dev, qa := seedCrew(st)
	rt.destroyErrByHandle = map[string]error{"h-qa": errors.New("tmux refused")}

	res, err := m.Teardown(ctx, dev.ID, domain.TerminationCauseKill)
	if err != nil {
		t.Fatalf("a crew member that would not die must not fail dev's teardown: %v", err)
	}
	if !st.sessions[dev.ID].IsTerminated {
		t.Fatal("dev must still terminate when its crew member cannot be reaped")
	}
	if st.sessions[qa.ID].IsTerminated {
		t.Fatal("the fake failed the member teardown, so it should still read as live")
	}
	if res.Freed {
		t.Fatal("dev removed the worktree while a member it could not reap was still in it")
	}
	if res.Reason != ReasonWorkspaceShared {
		t.Fatalf("reason = %q, want %q so the reclaim log explains the refusal", res.Reason, ReasonWorkspaceShared)
	}
	if ws.destroyed != 0 {
		t.Fatalf("workspace destroyed %d time(s)", ws.destroyed)
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

// TestTeardown_AutoReclaimEndsAnAbandonedHalfCrew is the answer to "what happens
// to a half-terminated crew that is then abandoned".
//
// The lifecycle reducer terminates dev directly when its PR merges — no teardown
// — so a task can reach "dev finished, subordinate still live". The idle sweep
// only ever SUSPENDS, so nothing else would ever terminate that subordinate, and
// it would pin the worktree forever: a silent disk leak. Auto-reclaim inherits
// the fan-out, so one grace period after the task ended the subordinate is ended
// with it and the tree is freed. The row it leaves names auto_reclaim, so who
// took it is answerable from the record.
func TestTeardown_AutoReclaimEndsAnAbandonedHalfCrew(t *testing.T) {
	m, st, _, ws := newManager()
	dev, qa := seedCrew(st)
	// dev merged and was terminated by the reducer; qa never heard about it.
	rec := st.sessions[dev.ID]
	rec.IsTerminated = true
	st.sessions[dev.ID] = rec

	res, err := m.Teardown(ctx, dev.ID, domain.TerminationCauseAutoReclaim)
	if err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if !res.Freed {
		t.Fatalf("an abandoned half-crew kept its worktree forever (reason %q)", res.Reason)
	}
	if ws.destroyed != 1 {
		t.Fatalf("workspace destroyed %d time(s), want 1", ws.destroyed)
	}
	if !st.sessions[qa.ID].IsTerminated {
		t.Fatal("the abandoned crew member is still live on a worktree that no longer exists")
	}
	lcm, ok := m.lcm.(*fakeLCM)
	if !ok {
		t.Fatal("expected the fake lifecycle recorder")
	}
	causes := lcm.terminationCauses[qa.ID]
	if len(causes) != 1 || causes[0] != domain.TerminationCauseAutoReclaim {
		t.Fatalf("crew member termination causes = %v, want [%s] so the record names who took it", causes, domain.TerminationCauseAutoReclaim)
	}
}

// TestTeardown_SubordinateNeverEndsItsDev is the other half of the rule, and the
// one that would be a disaster to get wrong: a qa session that finishes, is
// killed, or dies must never take the task down with it.
func TestTeardown_SubordinateNeverEndsItsDev(t *testing.T) {
	m, st, _, _ := newManager()
	dev, qa := seedCrew(st)

	for _, cause := range []string{domain.TerminationCauseKill, domain.TerminationCauseAutoReclaim} {
		st.sessions[qa.ID] = domain.SessionRecord{
			ID: qa.ID, ProjectID: "mer", Kind: domain.KindWorker,
			CrewID: dev.ID, CrewRole: domain.CrewRoleQA,
			Metadata: domain.SessionMetadata{Branch: "feature/task", WorkspacePath: crewPath, RuntimeHandleID: "h-qa"},
			Activity: domain.Activity{State: domain.ActivityActive},
		}
		if _, err := m.Teardown(ctx, qa.ID, cause); err != nil {
			t.Fatalf("Teardown(qa, %s): %v", cause, err)
		}
		if st.sessions[dev.ID].IsTerminated {
			t.Fatalf("tearing a subordinate down with cause %s terminated its dev", cause)
		}
	}
}
