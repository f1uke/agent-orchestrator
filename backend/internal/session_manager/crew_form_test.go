package sessionmanager

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// crewOf returns the (dev, qa) pair of a task, failing the test when the shape is
// not the one asked for.
func crewOf(t *testing.T, st *fakeStore, devID domain.SessionID) (domain.SessionRecord, domain.SessionRecord) {
	t.Helper()
	dev, ok := st.sessions[devID]
	if !ok {
		t.Fatalf("dev %s is not in the store", devID)
	}
	var qa domain.SessionRecord
	found := 0
	for _, rec := range st.sessions {
		if rec.ID == devID || rec.CrewID != dev.CrewID || dev.CrewID == "" {
			continue
		}
		qa = rec
		found++
	}
	if found != 1 {
		t.Fatalf("task %s has %d crewmates, want exactly 1 (rows: %d)", devID, found, len(st.sessions))
	}
	return dev, qa
}

// TestSpawn_StandardCreatesOneSession is lazy creation at the spawn seam: a
// `standard` task is ALLOWED a qa, and still comes out as ONE session. Nothing
// exists to be tested yet, so nothing is spent on testing it - and a task that
// never touches a runtime surface stays exactly this shape for ever.
func TestSpawn_StandardCreatesOneSession(t *testing.T) {
	for _, size := range []domain.TaskSize{domain.TaskSizeStandard, domain.TaskSizeDeep, ""} {
		t.Run(string(size)+"|", func(t *testing.T) {
			m, st, rt, ws := newManager()
			dev, err := m.Spawn(ctx, ports.SpawnConfig{
				ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build the thing", TaskSize: size,
			})
			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			if len(st.sessions) != 1 {
				t.Fatalf("a %q spawn produced %d rows, want exactly 1", size, len(st.sessions))
			}
			if dev.InCrew() {
				t.Fatalf("a fresh spawn is already in a crew: crew=%q role=%q", dev.CrewID, dev.CrewRole)
			}
			if rt.created != 1 || ws.createCalls != 1 {
				t.Fatalf("spawn touched the world %d/%d times, want 1/1", rt.created, ws.createCalls)
			}
		})
	}
}

// TestSpawn_MechanicalStaysSolo is the other half of the switch, and it is the
// hard requirement: a mechanical task is ONE row and ONE card, indistinguishable
// from every task on the board before this change.
func TestSpawn_MechanicalStaysSolo(t *testing.T) {
	m, st, rt, ws := newManager()
	rec, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "rename the flag",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(st.sessions) != 1 {
		t.Fatalf("mechanical spawn produced %d rows, want exactly 1", len(st.sessions))
	}
	if rec.InCrew() {
		t.Fatalf("mechanical spawn produced a crew member: crew=%q role=%q", rec.CrewID, rec.CrewRole)
	}
	if rt.created != 1 || ws.createCalls != 1 {
		t.Fatalf("mechanical spawn touched the world %d/%d times, want 1/1", rt.created, ws.createCalls)
	}
	if rt.lastCfg.Branch != rec.Metadata.Branch {
		t.Fatalf("mechanical runtime branch = %q, want the branch-named handle %q", rt.lastCfg.Branch, rec.Metadata.Branch)
	}
}

// TestSpawn_OrchestratorNeverGetsACrew: the crew is a shape for TASKS. An
// orchestrator shares one worktree with every other orchestrator of its project,
// so a crew there would be a category error.
func TestSpawn_OrchestratorNeverGetsACrew(t *testing.T) {
	m, st, _, _ := newManager()
	if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(st.sessions) != 1 {
		t.Fatalf("an orchestrator spawn produced %d rows, want 1", len(st.sessions))
	}
}

// TestStartTodo_StartsOneSessionWhateverItsSize: starting a staged task is an
// ordinary spawn, so it creates one session at every size. A qa arrives later or
// not at all, exactly as it would have for a direct spawn.
func TestStartTodo_StartsOneSessionWhateverItsSize(t *testing.T) {
	for _, size := range []domain.TaskSize{domain.TaskSizeStandard, domain.TaskSizeMechanical} {
		t.Run(string(size), func(t *testing.T) {
			m, st, _, _ := newManager()
			todo, err := m.PrepareTodo(ctx, ports.SpawnConfig{
				ProjectID: "mer", Kind: domain.KindWorker, Prompt: "staged work",
				Harness: domain.HarnessClaudeCode, TaskSize: size,
			})
			if err != nil {
				t.Fatalf("PrepareTodo: %v", err)
			}
			if len(st.sessions) != 1 {
				t.Fatalf("a staged TODO created %d rows, want 1 - nothing exists until it starts", len(st.sessions))
			}
			if _, err := m.StartTodo(ctx, todo.ID); err != nil {
				t.Fatalf("StartTodo: %v", err)
			}
			if len(st.sessions) != 1 {
				t.Fatalf("a started %q TODO is %d rows, want 1", size, len(st.sessions))
			}
		})
	}
}

// TestWakeCrewMember_StartsItWithoutStoppingDev. This is the shape in one test:
// starting qa is not a handover, it is a start. dev keeps its row, its agent and
// its tmux, and for the first time both members of the crew are awake at once in
// the one worktree.
func TestWakeCrewMember_StartsItWithoutStoppingDev(t *testing.T) {
	m, st, rt, _ := newManager()
	dev, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "work", TaskSize: domain.TaskSizeStandard,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// dev's agent is genuinely running: the wake routes probe a crewmate that
	// claims to be awake and put a CORPSE to sleep, which is the half of the old
	// guard that survives.
	if rt.aliveByHandle == nil {
		rt.aliveByHandle = map[string]bool{}
	}
	rt.aliveByHandle["h1"] = true

	if _, err := m.RequestCrewReview(ctx, dev.ID, domain.CrewRoleQA); err != nil {
		t.Fatalf("RequestCrewReview: %v", err)
	}
	_, qa := crewOf(t, st, dev.ID)
	// Put it back to sleep: this test is about the WAKE, and a member dev asked
	// for is already awake.
	if err := m.SuspendRuntime(ctx, qa.ID); err != nil {
		t.Fatalf("SuspendRuntime qa: %v", err)
	}

	woken, err := m.WakeCrewMember(ctx, qa.ID)
	if err != nil {
		t.Fatalf("WakeCrewMember: %v", err)
	}
	if woken.IsSuspended {
		t.Fatalf("qa is still suspended after being woken")
	}
	devAfter := st.sessions[dev.ID]
	if devAfter.IsSuspended || !devAfter.Awake() {
		t.Fatalf("starting qa stood dev down: suspended=%v awake=%v", devAfter.IsSuspended, devAfter.Awake())
	}
	if !rt.aliveByHandle[dev.Metadata.RuntimeHandleID] {
		t.Fatalf("dev's tmux was reaped when qa started")
	}
	awake := 0
	for _, rec := range st.sessions {
		if rec.CrewID == dev.ID && rec.Awake() {
			awake++
		}
	}
	if awake != 2 {
		t.Fatalf("%d members of the crew are awake, want both", awake)
	}

	// Starting a member that is already up is a no-op, not an error.
	if _, err := m.WakeCrewMember(ctx, qa.ID); err != nil {
		t.Fatalf("re-starting a running member: %v", err)
	}
	if st.sessions[qa.ID].IsSuspended {
		t.Fatalf("re-starting a running member put it back to sleep")
	}
}

// TestWakeCrewMember_RefusesASoloSession: the affordance is about a crew. A solo
// session has no crewmate to be named next to, and saying so is better than
// silently doing something else with somebody's only agent.
func TestWakeCrewMember_RefusesASoloSession(t *testing.T) {
	m, _, _, _ := newManager()
	rec, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "work", TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := m.WakeCrewMember(ctx, rec.ID); err == nil {
		t.Fatalf("WakeCrewMember accepted a solo session")
	}
}
