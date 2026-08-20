package sessionmanager

import (
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

// TestSpawn_StandardFormsACrew is the switch being ON: a task whose size says it
// wants a crew comes out as TWO sessions on ONE worktree, with dev holding the
// slot and qa asleep beside it.
func TestSpawn_StandardFormsACrew(t *testing.T) {
	for _, size := range []domain.TaskSize{domain.TaskSizeStandard, domain.TaskSizeDeep, ""} {
		t.Run(string(size)+"|", func(t *testing.T) {
			m, st, rt, ws := newManager()
			dev, err := m.Spawn(ctx, ports.SpawnConfig{
				ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build the thing", TaskSize: size,
			})
			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			devRow, qa := crewOf(t, st, dev.ID)

			if !devRow.CrewRole.IsDev() || devRow.CrewID != devRow.ID {
				t.Fatalf("dev row role=%q crew=%q, want dev/%s", devRow.CrewRole, devRow.CrewID, devRow.ID)
			}
			if qa.CrewRole != domain.CrewRoleQA || qa.CrewID != devRow.ID {
				t.Fatalf("qa row role=%q crew=%q, want qa/%s", qa.CrewRole, qa.CrewID, devRow.ID)
			}
			// BORN SUSPENDED: a row and an id, and nothing else. The runtime and the
			// workspace were touched exactly once - by dev.
			if !qa.IsSuspended || qa.Awake() {
				t.Fatalf("qa is not born suspended: suspended=%v awake=%v", qa.IsSuspended, qa.Awake())
			}
			if qa.Metadata.RuntimeHandleID != "" {
				t.Fatalf("qa took a runtime handle %q; it must have no tmux at all", qa.Metadata.RuntimeHandleID)
			}
			if rt.created != 1 {
				t.Fatalf("runtime created %d times, want 1 (dev only)", rt.created)
			}
			if ws.createCalls != 1 {
				t.Fatalf("workspace created %d times, want 1 (the tree is shared)", ws.createCalls)
			}
			// One worktree, one branch: the share the whole design rests on.
			if qa.Metadata.WorkspacePath != devRow.Metadata.WorkspacePath || qa.Metadata.Branch != devRow.Metadata.Branch {
				t.Fatalf("qa is not in dev's tree: %q@%q vs %q@%q",
					qa.Metadata.WorkspacePath, qa.Metadata.Branch, devRow.Metadata.WorkspacePath, devRow.Metadata.Branch)
			}
			// dev still holds the slot. Forming the crew must not have taken it.
			holder, ok, err := m.CrewSlotHolder(ctx, devRow.ID)
			if err != nil || !ok || holder.ID != devRow.ID {
				t.Fatalf("crew slot holder = %v/%v/%v, want dev %s", holder.ID, ok, err, devRow.ID)
			}
			// qa has a turn waiting for it: a promptless worker cannot be relaunched
			// at all (ErrNotResumable), so an empty prompt would make qa unwakeable.
			if !strings.Contains(qa.Metadata.Prompt, "build the thing") {
				t.Fatalf("qa's kickoff does not carry dev's brief:\n%s", qa.Metadata.Prompt)
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

// TestStartTodo_FormsTheCrewItsSizeAsksFor: a task staged as a TODO carries its
// size to Start, so it must come up with the same crew a direct spawn would give
// it - and a mechanical TODO must still come up alone.
func TestStartTodo_FormsTheCrewItsSizeAsksFor(t *testing.T) {
	t.Run("standard", func(t *testing.T) {
		m, st, _, _ := newManager()
		todo, err := m.PrepareTodo(ctx, ports.SpawnConfig{
			ProjectID: "mer", Kind: domain.KindWorker, Prompt: "staged work",
			Harness: domain.HarnessClaudeCode, TaskSize: domain.TaskSizeStandard,
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
		_, qa := crewOf(t, st, todo.ID)
		if !qa.IsSuspended {
			t.Fatalf("a started TODO's qa is awake; it must be born suspended")
		}
	})
	t.Run("mechanical", func(t *testing.T) {
		m, st, _, _ := newManager()
		todo, err := m.PrepareTodo(ctx, ports.SpawnConfig{
			ProjectID: "mer", Kind: domain.KindWorker, Prompt: "staged tweak",
			Harness: domain.HarnessClaudeCode, TaskSize: domain.TaskSizeMechanical,
		})
		if err != nil {
			t.Fatalf("PrepareTodo: %v", err)
		}
		if _, err := m.StartTodo(ctx, todo.ID); err != nil {
			t.Fatalf("StartTodo: %v", err)
		}
		if len(st.sessions) != 1 {
			t.Fatalf("a mechanical TODO started as %d rows, want 1", len(st.sessions))
		}
	})
}

// TestFormCrew_IsBestEffort: dev is already running with a worktree by the time
// the crew is formed, so a failure to add qa must leave a working solo task
// behind rather than rolling the whole spawn back.
func TestFormCrew_IsBestEffort(t *testing.T) {
	m, st, _, _ := newManager()
	st.failCreateAfter = 1 // dev's row lands; qa's create explodes

	rec, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "work", TaskSize: domain.TaskSizeStandard,
	})
	if err != nil {
		t.Fatalf("Spawn must survive a crew that could not form: %v", err)
	}
	if rec.InCrew() {
		t.Fatalf("dev claims a crew that was never formed: crew=%q role=%q", rec.CrewID, rec.CrewRole)
	}
	if len(st.sessions) != 1 {
		t.Fatalf("a failed crew left %d rows behind, want 1", len(st.sessions))
	}
}

// TestWakeCrewMember_GoesThroughTheExclusion: waking qa is a HANDOVER, not a
// second agent. dev must be asleep with its tmux gone before qa is up, and the
// slot must hold exactly one member at every observable moment.
func TestWakeCrewMember_GoesThroughTheExclusion(t *testing.T) {
	m, st, rt, _ := newManager()
	dev, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "work", TaskSize: domain.TaskSizeStandard,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	_, qa := crewOf(t, st, dev.ID)

	woken, err := m.WakeCrewMember(ctx, qa.ID)
	if err != nil {
		t.Fatalf("WakeCrewMember: %v", err)
	}
	if woken.IsSuspended {
		t.Fatalf("qa is still suspended after being woken")
	}
	devAfter := st.sessions[dev.ID]
	if !devAfter.IsSuspended {
		t.Fatalf("dev was not stood down; two members would be awake in one checkout")
	}
	if rt.aliveByHandle[dev.Metadata.RuntimeHandleID] {
		t.Fatalf("dev's tmux is still alive after the handover")
	}
	holder, ok, err := m.CrewSlotHolder(ctx, qa.ID)
	if err != nil || !ok || holder.ID != qa.ID {
		t.Fatalf("crew slot holder = %v/%v/%v, want qa %s", holder.ID, ok, err, qa.ID)
	}

	// Waking the holder again is a no-op, not an error: "qa's turn" when it is
	// already qa's turn should not cost the human a toast.
	if _, err := m.WakeCrewMember(ctx, qa.ID); err != nil {
		t.Fatalf("re-waking the holder: %v", err)
	}
	if st.sessions[qa.ID].IsSuspended {
		t.Fatalf("re-waking the holder put it back to sleep")
	}
}

// TestWakeCrewMember_RefusesASoloSession: the affordance is about a crew. A solo
// session has no slot to hand over, and saying so is better than silently doing
// something else.
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
