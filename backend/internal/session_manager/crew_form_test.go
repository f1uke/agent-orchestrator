package sessionmanager

import (
	"errors"
	"strings"
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

	woken, _, err := m.WakeCrewMember(ctx, qa.ID)
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
	if _, _, err := m.WakeCrewMember(ctx, qa.ID); err != nil {
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
	if _, _, err := m.WakeCrewMember(ctx, rec.ID); err == nil {
		t.Fatalf("WakeCrewMember accepted a solo session")
	}
}

// TestWakeCrewMember_RestoresAFinishedMember is the whole point of the second
// round: a qa that closed its round is not a dead end.
//
// It is deliberately asserted through `wake` rather than through Restore. The
// capability was always there - `ao session restore` does exactly this - and the
// gap was that the verb a person reaches for ("bring qa up") refused, naming a
// state and no way out of it. So the test is that the verb works, and that it
// SAYS it did something bigger than start a paused agent.
func TestWakeCrewMember_RestoresAFinishedMember(t *testing.T) {
	m, st, rt, _ := newManager()
	dev, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "work", TaskSize: domain.TaskSizeStandard,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if rt.aliveByHandle == nil {
		rt.aliveByHandle = map[string]bool{}
	}
	rt.aliveByHandle["h1"] = true
	if _, err := m.RequestCrewReview(ctx, dev.ID, domain.CrewRoleQA); err != nil {
		t.Fatalf("RequestCrewReview: %v", err)
	}
	_, qa := crewOf(t, st, dev.ID)

	// qa finished its round. dev is still live, still in the worktree, and that
	// worktree is what qa comes back into.
	rec := st.sessions[qa.ID]
	rec.IsTerminated = true
	rec.Activity = domain.Activity{State: domain.ActivityExited}
	st.sessions[qa.ID] = rec

	woken, restored, err := m.WakeCrewMember(ctx, qa.ID)
	if err != nil {
		t.Fatalf("waking a finished qa: %v", err)
	}
	if !restored {
		t.Fatalf("a finished qa came back without the caller being told it was a restore")
	}
	if woken.IsTerminated {
		t.Fatalf("qa is still terminated after being woken")
	}
	if st.sessions[dev.ID].IsSuspended {
		t.Fatalf("bringing qa back stood dev down; both members work at once")
	}
	// And the round boundary is recorded, which is what lets dev brief it about
	// the same unchanged commit the last round used up its budget on.
	if st.sessions[qa.ID].CrewRoundStartedAt.IsZero() {
		t.Fatalf("a restored member starts no new round, so its next brief is still capped on last round's subject")
	}
}

// TestWakeCrewMember_RefusesWhenEveryMemberHasFinished draws the line.
//
// A crew shares ONE worktree and it is dev's: a finished qa beside a live dev is
// a row without a runtime next to a tree that is still standing, and restoring
// it re-attaches. When every member has ended, that tree came down with the last
// of them, and Restore would not re-attach to anything - it would cut the branch
// a worktree again and stand a lone agent up in a task that is over. That is a
// much bigger act than the word "wake" promises, so it is refused and left to
// `ao session restore`, which the message names.
func TestWakeCrewMember_RefusesWhenEveryMemberHasFinished(t *testing.T) {
	m, st, rt, _ := newManager()
	dev, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "work", TaskSize: domain.TaskSizeStandard,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if rt.aliveByHandle == nil {
		rt.aliveByHandle = map[string]bool{}
	}
	rt.aliveByHandle["h1"] = true
	if _, err := m.RequestCrewReview(ctx, dev.ID, domain.CrewRoleQA); err != nil {
		t.Fatalf("RequestCrewReview: %v", err)
	}
	_, qa := crewOf(t, st, dev.ID)
	for _, id := range []domain.SessionID{dev.ID, qa.ID} {
		rec := st.sessions[id]
		rec.IsTerminated = true
		rec.Activity = domain.Activity{State: domain.ActivityExited}
		st.sessions[id] = rec
	}

	_, _, err = m.WakeCrewMember(ctx, qa.ID)
	if err == nil {
		t.Fatalf("wake resurrected a task every member of which had finished")
	}
	if !errors.Is(err, ErrInvalidCrew) {
		t.Fatalf("refusal is %v, want ErrInvalidCrew", err)
	}
	// The refusal has to carry the way out, which is the whole reason this
	// capability was unfindable: three errors reported a state and stopped.
	if !strings.Contains(err.Error(), "ao session restore "+string(dev.ID)) {
		t.Fatalf("refusal does not name the command that gets past it:\n%s", err)
	}
	if st.sessions[qa.ID].IsTerminated != true {
		t.Fatalf("a refused wake changed the row anyway")
	}
}
