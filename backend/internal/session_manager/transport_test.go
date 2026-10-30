package sessionmanager

import (
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type chatFixture struct {
	m       *Manager
	st      *fakeStore
	rt      *fakeRuntime
	agent   *recordingAgent
	dataDir string
}

func newChatFixture(t *testing.T) chatFixture {
	t.Helper()
	f := chatFixture{st: newFakeStore(), rt: &fakeRuntime{}, agent: &recordingAgent{}, dataDir: t.TempDir()}
	f.st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	f.m = New(Deps{
		Runtime: f.rt, Agents: singleAgent{agent: f.agent}, Workspace: &fakeWorkspace{path: t.TempDir()},
		Store: f.st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: f.st},
		DataDir: f.dataDir, LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	return f
}

func TestChatTransportNeedsClaudeCode(t *testing.T) {
	f := newChatFixture(t)
	assertRefused := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, ErrTransportUnsupported) || !strings.Contains(err.Error(), "chat") || !strings.Contains(err.Error(), "codex") {
			t.Fatalf("err = %v, want ErrTransportUnsupported naming chat and codex", err)
		}
	}
	_, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Prompt: "do it", Transport: domain.TransportChat})
	assertRefused(t, err)
	_, err = f.m.PrepareTodo(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Prompt: "do it", Transport: domain.TransportChat})
	assertRefused(t, err)
	if len(f.st.sessions) != 0 || f.rt.created != 0 {
		t.Fatalf("a refused spawn left %d rows and %d runtimes", len(f.st.sessions), f.rt.created)
	}

	todo, err := f.m.PrepareTodo(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it", Transport: domain.TransportChat})
	if err != nil {
		t.Fatal(err)
	}
	codex := domain.HarnessCodex
	if _, err := f.m.UpdateTodoSpec(ctx, todo.ID, ports.TodoSpecPatch{Harness: &codex}); err != nil {
		t.Fatal(err)
	}
	_, err = f.m.StartTodo(ctx, todo.ID)
	assertRefused(t, err)
	if f.rt.created != 0 {
		t.Fatal("a refused Start launched a runtime")
	}
}
