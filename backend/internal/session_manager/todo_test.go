package sessionmanager

import (
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func strp(s string) *string { return &s }

// PrepareTodo persists the full spec and marks the row is_todo WITHOUT creating
// any workspace or runtime.
func TestPrepareTodo_PersistsSpecWithoutMaterializing(t *testing.T) {
	m, st, rt, ws := newManager()

	rec, err := m.PrepareTodo(ctx, ports.SpawnConfig{
		ProjectID:      "mer",
		Kind:           domain.KindWorker,
		Harness:        domain.HarnessClaudeCode,
		Branch:         "feature/x",
		BaseBranch:     "main-fluke",
		AutoNameBranch: false,
		PRTarget:       "main-fluke",
		Prompt:         "do the thing",
		DisplayName:    "board todo",
		CreatedBy:      "mer-1",
	})
	if err != nil {
		t.Fatalf("PrepareTodo: %v", err)
	}
	if !rec.IsTodo {
		t.Fatal("prepared record is not marked IsTodo")
	}
	if ws.createCalls != 0 {
		t.Fatalf("workspace created = %d, want 0 (deferred)", ws.createCalls)
	}
	if rt.created != 0 {
		t.Fatalf("runtime created = %d, want 0 (deferred)", rt.created)
	}
	stored := st.sessions[rec.ID]
	if !stored.IsTodo || stored.BaseBranch != "main-fluke" || stored.PRTarget != "main-fluke" ||
		stored.Metadata.Branch != "feature/x" || stored.Metadata.Prompt != "do the thing" ||
		stored.CreatedBy != "mer-1" || stored.DisplayName != "board todo" {
		t.Fatalf("stored spec not persisted verbatim: %#v", stored)
	}
}

// PrepareTodo allows an empty harness (resolved to the project default at
// Start) but rejects an unknown non-empty one.
func TestPrepareTodo_HarnessValidation(t *testing.T) {
	// An empty harness is allowed (resolves to the project default at Start).
	m, _, _, _ := newManager()
	if _, err := m.PrepareTodo(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatalf("empty harness should be allowed on a TODO: %v", err)
	}

	// A non-empty but unregistered harness is rejected up front.
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	mNoAgents := New(Deps{Runtime: &fakeRuntime{}, Agents: missingAgents{}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil }})
	if _, err := mNoAgents.PrepareTodo(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: "bogus"}); !errors.Is(err, ErrUnknownHarness) {
		t.Fatalf("unknown harness err = %v, want ErrUnknownHarness", err)
	}
}

// StartTodo materializes the prepared row in place (same id), creates the
// workspace + runtime, and clears is_todo.
func TestStartTodo_MaterializesInPlace(t *testing.T) {
	m, st, rt, ws := newManager()

	prepared, err := m.PrepareTodo(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Branch: "feature/x", BaseBranch: "main-fluke", Prompt: "go",
	})
	if err != nil {
		t.Fatalf("PrepareTodo: %v", err)
	}

	started, err := m.StartTodo(ctx, prepared.ID)
	if err != nil {
		t.Fatalf("StartTodo: %v", err)
	}
	if started.ID != prepared.ID {
		t.Fatalf("started id = %s, want same as todo id %s", started.ID, prepared.ID)
	}
	if started.IsTodo {
		t.Fatal("started record still marked IsTodo")
	}
	if ws.createCalls != 1 {
		t.Fatalf("workspace created = %d, want 1", ws.createCalls)
	}
	if rt.created != 1 {
		t.Fatalf("runtime created = %d, want 1", rt.created)
	}
	if st.sessions[prepared.ID].IsTodo {
		t.Fatal("stored row still IsTodo after Start")
	}
}

// A TODO staged with `--task-size mechanical` must persist the size and, when
// Started, launch the worker with the mechanical skip authorization in its system
// prompt — proving TaskSize survives PrepareTodo -> StartTodo (the prompt is
// derived from the replayed spec, not the empty spawn cfg).
func TestStartTodo_ReplaysMechanicalTaskSizeIntoPrompt(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	agent := &recordingAgent{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	prepared, err := m.PrepareTodo(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Branch: "feature/x", BaseBranch: "main-fluke", Prompt: "rename it",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("PrepareTodo: %v", err)
	}
	if got := st.sessions[prepared.ID].TaskSize; got != domain.TaskSizeMechanical {
		t.Fatalf("stored TODO TaskSize = %q, want mechanical", got)
	}

	if _, err := m.StartTodo(ctx, prepared.ID); err != nil {
		t.Fatalf("StartTodo: %v", err)
	}
	if !strings.Contains(agent.lastLaunch.SystemPrompt, "## Task size: mechanical (AO)") {
		t.Fatalf("started TODO lost the mechanical directive:\n%s", agent.lastLaunch.SystemPrompt)
	}
}

func TestStartTodo_RejectsNonTodo(t *testing.T) {
	m, _, _, _ := newManager()

	// A normally-spawned (live) session is not a TODO.
	live, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := m.StartTodo(ctx, live.ID); !errors.Is(err, ErrNotTodo) {
		t.Fatalf("StartTodo on live session err = %v, want ErrNotTodo", err)
	}
	if _, err := m.StartTodo(ctx, "mer-999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("StartTodo on missing id err = %v, want ErrNotFound", err)
	}
}

// A materialize failure keeps the row queued in TODO for a retry (not deleted,
// not terminated).
func TestStartTodo_FailureKeepsTodo(t *testing.T) {
	m, st, rt, _ := newManager()
	rt.createErr = errors.New("tmux boom")

	prepared, err := m.PrepareTodo(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Prompt: "go",
	})
	if err != nil {
		t.Fatalf("PrepareTodo: %v", err)
	}
	if _, err := m.StartTodo(ctx, prepared.ID); err == nil {
		t.Fatal("StartTodo should have failed on runtime error")
	}
	row, ok := st.sessions[prepared.ID]
	if !ok {
		t.Fatal("todo row was deleted on failed Start; should be kept for retry")
	}
	if !row.IsTodo {
		t.Fatal("todo row lost IsTodo on failed Start")
	}
	if row.IsTerminated {
		t.Fatal("todo row was terminated on failed Start; should stay queued")
	}
}

func TestUpdateTodoSpec_EditsFields(t *testing.T) {
	m, st, _, _ := newManager()

	prepared, err := m.PrepareTodo(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Branch: "feature/x", BaseBranch: "main", Prompt: "old", DisplayName: "old-name",
	})
	if err != nil {
		t.Fatalf("PrepareTodo: %v", err)
	}

	updated, err := m.UpdateTodoSpec(ctx, prepared.ID, ports.TodoSpecPatch{
		DisplayName: strp("new-name"),
		Branch:      strp("feature/y"),
		BaseBranch:  strp("main-fluke"),
		PRTarget:    strp("main-fluke"),
		Prompt:      strp("new prompt"),
	})
	if err != nil {
		t.Fatalf("UpdateTodoSpec: %v", err)
	}
	if updated.DisplayName != "new-name" {
		t.Fatalf("display name = %q, want new-name", updated.DisplayName)
	}
	stored := st.sessions[prepared.ID]
	if stored.Metadata.Branch != "feature/y" || stored.BaseBranch != "main-fluke" ||
		stored.PRTarget != "main-fluke" || stored.Metadata.Prompt != "new prompt" {
		t.Fatalf("spec edits not persisted: %#v", stored)
	}
	if !stored.IsTodo {
		t.Fatal("editing a TODO must not clear IsTodo")
	}
}

func TestUpdateTodoSpec_RejectsNonTodo(t *testing.T) {
	m, _, _, _ := newManager()
	live, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := m.UpdateTodoSpec(ctx, live.ID, ports.TodoSpecPatch{Prompt: strp("x")}); !errors.Is(err, ErrNotTodo) {
		t.Fatalf("UpdateTodoSpec on live session err = %v, want ErrNotTodo", err)
	}
}
