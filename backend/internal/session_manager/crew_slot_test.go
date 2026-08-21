package sessionmanager

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestReconcileCrewPeers_IsFreeForASoloSession is the preservation guard at its
// cheapest: a session in no crew must not cost a single query or probe. Solo is
// the zero value, and this is what keeps it a no-op path rather than a branch
// somebody has to remember to keep correct.
func TestReconcileCrewPeers_IsFreeForASoloSession(t *testing.T) {
	m, st, rt, _ := newManager()
	solo := mkLive("mer-1")
	st.sessions[solo.ID] = solo

	m.reconcileCrewPeers(ctx, solo, routeResume)

	if st.listAllCalls != 0 {
		t.Fatalf("a solo session cost %d session-list reads; want 0", st.listAllCalls)
	}
	if rt.aliveCalls != 0 {
		t.Fatalf("a solo session cost %d runtime probes; want 0", rt.aliveCalls)
	}
}

// TestReconcileCrewPeers_LeavesALiveCrewmateAlone. This is the whole change in
// one assertion: the crewmate is AWAKE AND RUNNING, and the member coming up is
// allowed to come up anyway. Under #225 this was the refusal.
func TestReconcileCrewPeers_LeavesALiveCrewmateAlone(t *testing.T) {
	m, st, rt, ws := newManager()
	dev := seedCrewDev(m, st, rt, ws)
	qa := mkLive("mer-2")
	qa.CrewID, qa.CrewRole = dev.ID, domain.CrewRoleQA
	st.sessions[qa.ID] = qa
	devRow := st.sessions[dev.ID]
	devRow.CrewID, devRow.CrewRole = dev.ID, domain.CrewRoleDev
	st.sessions[dev.ID] = devRow
	rt.aliveByHandle["h1"] = true

	m.reconcileCrewPeers(ctx, st.sessions[qa.ID], routeResume)

	if got := st.sessions[dev.ID]; got.IsSuspended || !got.Awake() {
		t.Fatalf("bringing qa up put a live dev to sleep: suspended=%v awake=%v", got.IsSuspended, got.Awake())
	}
}

// TestReconcileCrewPeers_SleepsACorpse. The refusal is gone but the PROBE is not,
// and this is why: a member whose agent died still reads as awake off its row,
// and "nobody is working on this" is derived from exactly that column. A crew
// showing a dead member as working is the lie that rule exists to catch.
func TestReconcileCrewPeers_SleepsACorpse(t *testing.T) {
	m, st, rt, ws := newManager()
	dev := seedCrewDev(m, st, rt, ws)
	qa := mkLive("mer-2")
	qa.CrewID, qa.CrewRole = dev.ID, domain.CrewRoleQA
	st.sessions[qa.ID] = qa
	devRow := st.sessions[dev.ID]
	devRow.CrewID, devRow.CrewRole = dev.ID, domain.CrewRoleDev
	st.sessions[dev.ID] = devRow
	rt.aliveByHandle["h1"] = false // dev's pane is gone

	m.reconcileCrewPeers(ctx, st.sessions[qa.ID], routeResume)

	got := st.sessions[dev.ID]
	if !got.IsSuspended {
		t.Fatal("a member whose runtime is gone still reads as awake")
	}
	if got.SleepReason != domain.SleepReasonIdle {
		t.Fatalf("sleep reason = %q, want idle: there are no turns to be waiting for", got.SleepReason)
	}
}

// TestReconcileCrewPeers_AnUnprobeableCrewmateIsLeftAlone. A failed probe is
// never proof of death - a load-bearing rule everywhere else in this daemon -
// and the cost of guessing wrong is now only a stale row the next route settles.
func TestReconcileCrewPeers_AnUnprobeableCrewmateIsLeftAlone(t *testing.T) {
	m, st, rt, ws := newManager()
	dev := seedCrewDev(m, st, rt, ws)
	qa := mkLive("mer-2")
	qa.CrewID, qa.CrewRole = dev.ID, domain.CrewRoleQA
	st.sessions[qa.ID] = qa
	devRow := st.sessions[dev.ID]
	devRow.CrewID, devRow.CrewRole = dev.ID, domain.CrewRoleDev
	st.sessions[dev.ID] = devRow
	rt.aliveErr = errors.New("tmux unreachable")

	m.reconcileCrewPeers(ctx, st.sessions[qa.ID], routeResume)

	if st.sessions[dev.ID].IsSuspended {
		t.Fatal("an unprobeable member was declared dead and put to sleep")
	}
}

// TestSpawnCrewMember_IsAllowedWhileDevIsAwake is the inverse of what #225
// asserted, at the spawn route. dev is normally the one working in the tree the
// new member is about to join, so a rule that waited for a quiet one would mean
// a crew could essentially never be formed.
func TestSpawnCrewMember_IsAllowedWhileDevIsAwake(t *testing.T) {
	m, st, rt, ws := newManager()
	dev := seedCrewDev(m, st, rt, ws)
	if dev.InCrew() {
		t.Fatal("precondition: dev is still solo when the crew spawn arrives")
	}
	rt.aliveByHandle["h1"] = true

	qa, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "test the task",
		CrewOf: dev.ID, CrewRole: domain.CrewRoleQA,
	})
	if err != nil {
		t.Fatalf("crew spawn into a running dev's tree: %v", err)
	}
	if got := st.sessions[dev.ID]; got.IsSuspended {
		t.Fatal("forming the crew stood dev down")
	}
	if !qa.InCrew() || qa.CrewID != dev.ID {
		t.Fatalf("the new member is not on dev's task: %q/%q", qa.CrewID, qa.CrewRole)
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
