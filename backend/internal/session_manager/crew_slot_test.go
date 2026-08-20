package sessionmanager

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestCrewSlotGuard_IsFreeForASoloSession is the preservation guard at its
// cheapest: a session in no crew must not cost the guard a single query or probe.
// Solo is the zero value, and this is what keeps it a no-op path rather than a
// branch somebody has to remember to keep correct.
func TestCrewSlotGuard_IsFreeForASoloSession(t *testing.T) {
	m, st, rt, _ := newManager()
	solo := mkLive("mer-1")
	st.sessions[solo.ID] = solo

	if err := m.crewSlotGuard(ctx, solo, routeResume); err != nil {
		t.Fatalf("the guard refused a solo session: %v", err)
	}
	if st.listAllCalls != 0 {
		t.Fatalf("the guard read the session list %d times for a solo session; want 0", st.listAllCalls)
	}
	if rt.aliveCalls != 0 {
		t.Fatalf("the guard probed a runtime %d times for a solo session; want 0", rt.aliveCalls)
	}
	if holder, ok, err := m.CrewSlotHolder(ctx, solo.ID); err != nil || ok {
		t.Fatalf("CrewSlotHolder for a solo session = %s (%v), %v; want none", holder.ID, ok, err)
	}
}

// TestReleaseCrewSlot_RefusesAnOrchestrator. Every orchestrator of a project
// already shares ONE worktree with every other orchestrator of that project, and
// they are not task members. Standing one down in the name of a crew's slot would
// be suspending an unrelated session.
func TestReleaseCrewSlot_RefusesAnOrchestrator(t *testing.T) {
	m, st, _, _ := newManager()
	orch := mkLive("mer-1")
	orch.Kind = domain.KindOrchestrator
	st.sessions[orch.ID] = orch

	if err := m.ReleaseCrewSlot(ctx, orch.ID); !errors.Is(err, ErrInvalidCrew) {
		t.Fatalf("ReleaseCrewSlot(orchestrator) = %v, want ErrInvalidCrew", err)
	}
	if st.sessions[orch.ID].IsSuspended {
		t.Fatal("the refused release still suspended the orchestrator")
	}
}

// TestReleaseCrewSlot_IsIdempotent: a caller may release unconditionally, which
// is what lets a handover put the release first without checking anything.
func TestReleaseCrewSlot_IsIdempotent(t *testing.T) {
	m, st, rt, ws := newManager()
	dev := seedCrewDev(m, st, rt, ws)

	for i := range 2 {
		if err := m.ReleaseCrewSlot(ctx, dev.ID); err != nil {
			t.Fatalf("release %d: %v", i+1, err)
		}
	}
	if !st.sessions[dev.ID].IsSuspended {
		t.Fatal("the release did not suspend the member")
	}
	if rt.destroyed != 1 {
		t.Fatalf("runtime destroyed %d times across two releases, want 1", rt.destroyed)
	}
}

// TestCrewDevsFirst_LeavesASoloListAlone: the boot restore pass on an ordinary
// machine must iterate exactly the slice it always did, by identity, so there is
// no ordering change to reason about where there is no crew.
func TestCrewDevsFirst_LeavesASoloListAlone(t *testing.T) {
	recs := []domain.SessionRecord{mkLive("mer-1"), mkLive("mer-2"), mkLive("mer-3")}
	got := crewDevsFirst(recs)
	if &got[0] != &recs[0] {
		t.Fatal("a list with no crew in it was copied or reordered")
	}
}

// TestCrewDevsFirst_PutsDevInFront. Only one member can carry a restore marker
// under this rule, but a database written before it can hold two - and then the
// FIRST one restored wins. dev owns the branch, the PR and the report, so dev is
// the one that should still be running afterwards.
func TestCrewDevsFirst_PutsDevInFront(t *testing.T) {
	qa := mkLive("mer-2")
	qa.CrewID, qa.CrewRole = "mer-1", domain.CrewRoleQA
	dev := mkLive("mer-1")
	dev.CrewID, dev.CrewRole = "mer-1", domain.CrewRoleDev
	other := mkLive("mer-3")

	got := crewDevsFirst([]domain.SessionRecord{qa, other, dev})
	if got[0].ID != dev.ID {
		t.Fatalf("order = %s, %s, %s; want dev first", got[0].ID, got[1].ID, got[2].ID)
	}
	// Everything that is not a crew dev keeps its relative order.
	if got[1].ID != qa.ID || got[2].ID != other.ID {
		t.Fatalf("the non-dev tail was reordered: %s, %s", got[1].ID, got[2].ID)
	}
}

// TestSpawnCrewMember_RefusedWhileDevIsAwake is the spawn route at the unit level:
// the guard has to look at the crew that is ABOUT to exist, because dev is still
// SOLO at this moment - recordCrew writes the membership only after the new member
// materializes. Asking dev whether it is "in a crew" answers no, and a guard that
// stopped there would wave every crew spawn through.
func TestSpawnCrewMember_RefusedWhileDevIsAwake(t *testing.T) {
	m, st, rt, ws := newManager()
	dev := seedCrewDev(m, st, rt, ws)
	if dev.InCrew() {
		t.Fatal("precondition: dev must still be solo when the guard runs")
	}

	_, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "test the task",
		CrewOf: dev.ID, CrewRole: domain.CrewRoleQA,
	})
	if !errors.Is(err, ErrCrewBusy) {
		t.Fatalf("crew spawn into a running dev's tree = %v, want ErrCrewBusy", err)
	}
	if rt.created != 0 {
		t.Fatal("the refused spawn launched an agent into the shared tree")
	}
	if got := st.sessions[dev.ID]; got.InCrew() {
		t.Fatalf("the refused spawn recorded a crew on dev: %q/%q", got.CrewID, got.CrewRole)
	}
}
