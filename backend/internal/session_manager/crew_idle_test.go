package sessionmanager

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// newCrewIdleManager arms the idle sweep and registers the project a crew spawn
// needs. It wraps the package's newIdleManager so the crew cases use the same
// clock and TTL wiring every other idle test does.
func newCrewIdleManager(ttl time.Duration, now time.Time) (*Manager, *fakeStore, *fakeRuntime, *fakeWorkspace) {
	m, st, rt, ws, _ := newIdleManager(ttl, now)
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	return m, st, rt, ws
}

// TestCloseIdleSessions_SuspendingOneCrewMemberLeavesTheOtherRunning is the
// stranding hazard the crew shape creates for the idle sweep.
//
// tmux names a session after project+branch, and a crew shares a branch: without
// per-member runtime naming the two would sit in ONE pane, and suspending the
// idle member would destroy the pane the working member is in. The task would
// look alive on the board and be dead underneath. Crew members get session-id
// handles, so the sweep reaps only what it means to.
func TestCloseIdleSessions_SuspendingOneCrewMemberLeavesTheOtherRunning(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	m, st, rt, ws := newCrewIdleManager(72*time.Hour, now)
	dev, qa := seedCrew(st)

	// qa has been quiet for a week; dev is working right now.
	live := st.sessions[dev.ID]
	live.Activity = domain.Activity{State: domain.ActivityActive, LastActivityAt: now.Add(-time.Minute)}
	st.sessions[dev.ID] = live
	idle := st.sessions[qa.ID]
	idle.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-7 * 24 * time.Hour)}
	st.sessions[qa.ID] = idle

	if err := m.CloseIdleSessions(ctx); err != nil {
		t.Fatalf("CloseIdleSessions: %v", err)
	}

	if !st.sessions[qa.ID].IsSuspended {
		t.Fatal("the idle crew member was not suspended")
	}
	if st.sessions[dev.ID].IsSuspended || st.sessions[dev.ID].IsTerminated {
		t.Fatal("the sweep suspended or terminated the crew member that is actively working")
	}
	for _, id := range rt.destroyedIDs {
		if id == "h-dev" {
			t.Fatalf("the sweep reaped the working member's runtime: destroyed %v", rt.destroyedIDs)
		}
	}
	if ws.destroyed != 0 {
		t.Fatalf("the idle sweep destroyed %d worktree(s); it must only ever suspend", ws.destroyed)
	}
	if got := st.sessions[qa.ID].Metadata.WorkspacePath; got != crewPath {
		t.Fatalf("suspended member workspace = %q, want it kept at %q so it resumes in place", got, crewPath)
	}
}

// TestCloseIdleSessions_WholeCrewIdleKeepsTheWorktree. When a task is abandoned,
// both members age out. They are SUSPENDED, not terminated, and the worktree
// stays on disk - exactly what an abandoned solo session does today, so the task
// remains resumable and reclaim is still not entitled to it.
func TestCloseIdleSessions_WholeCrewIdleKeepsTheWorktree(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	m, st, _, ws := newCrewIdleManager(72*time.Hour, now)
	dev, qa := seedCrew(st)
	for _, id := range []domain.SessionID{dev.ID, qa.ID} {
		rec := st.sessions[id]
		rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-7 * 24 * time.Hour)}
		st.sessions[id] = rec
	}

	if err := m.CloseIdleSessions(ctx); err != nil {
		t.Fatalf("CloseIdleSessions: %v", err)
	}
	for _, id := range []domain.SessionID{dev.ID, qa.ID} {
		rec := st.sessions[id]
		if !rec.IsSuspended {
			t.Fatalf("%s was not suspended", id)
		}
		if rec.IsTerminated {
			t.Fatalf("%s was terminated; the idle sweep must only suspend", id)
		}
		if rec.Metadata.WorkspacePath != crewPath {
			t.Fatalf("%s lost its workspace path", id)
		}
	}
	if ws.destroyed != 0 {
		t.Fatalf("the idle sweep destroyed %d worktree(s)", ws.destroyed)
	}
}

// TestCloseIdleSessions_SoloIsUnchanged is the preservation guard for the sweep:
// a solo session past the TTL is suspended, its own tmux reaped, its worktree
// kept - byte for byte what it does today.
func TestCloseIdleSessions_SoloIsUnchanged(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	m, st, rt, ws := newCrewIdleManager(72*time.Hour, now)
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{Branch: "feature/solo", WorkspacePath: "/ws/feature/solo", RuntimeHandleID: "h1"},
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-7 * 24 * time.Hour)},
	}
	rt.aliveByHandle["h1"] = true

	if err := m.CloseIdleSessions(ctx); err != nil {
		t.Fatalf("CloseIdleSessions: %v", err)
	}
	rec := st.sessions["mer-1"]
	if !rec.IsSuspended || rec.IsTerminated {
		t.Fatalf("solo idle session = suspended %v terminated %v, want suspended only", rec.IsSuspended, rec.IsTerminated)
	}
	if len(rt.destroyedIDs) != 1 || rt.destroyedIDs[0] != "h1" {
		t.Fatalf("runtimes destroyed = %v, want [h1]", rt.destroyedIDs)
	}
	if ws.destroyed != 0 {
		t.Fatalf("the idle sweep destroyed %d worktree(s)", ws.destroyed)
	}
	if rec.Metadata.WorkspacePath != "/ws/feature/solo" {
		t.Fatalf("workspace path = %q, want it kept", rec.Metadata.WorkspacePath)
	}
}

// TestReconcile_IdleCrewMemberSuspendsInsteadOfTearingTheTreeDown covers the boot
// pass for the other branch of reconcileLive: a member idle past the TTL whose
// runtime died is suspended, which keeps the shared worktree, rather than
// captured-and-removed.
func TestReconcile_IdleCrewMemberSuspendsInsteadOfTearingTheTreeDown(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	m, st, rt, ws := newCrewIdleManager(72*time.Hour, now)
	dev, qa := seedCrew(st)
	rt.aliveByHandle["h-dev"] = true
	idle := st.sessions[qa.ID]
	idle.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-7 * 24 * time.Hour)}
	st.sessions[qa.ID] = idle
	live := st.sessions[dev.ID]
	live.Activity = domain.Activity{State: domain.ActivityActive, LastActivityAt: now.Add(-time.Minute)}
	st.sessions[dev.ID] = live

	if err := m.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !st.sessions[qa.ID].IsSuspended {
		t.Fatal("the idle crew member should have been suspended on boot")
	}
	if st.sessions[dev.ID].IsTerminated || st.sessions[dev.ID].IsSuspended {
		t.Fatal("boot took out the crew member whose runtime survived")
	}
	if ws.stashCalls != 0 {
		t.Fatalf("boot stashed the shared worktree %d time(s) on the idle member's behalf", ws.stashCalls)
	}
}
