package sessionmanager

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/promptfile"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

const daemonExe = "/opt/ao/bin/ao"

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
		Executable: func() (string, error) { return daemonExe, nil },
	})
	t.Cleanup(func() {
		_, sock := sessionhost.Paths(f.dataDir, "mer-1")
		if !strings.HasPrefix(sock, f.dataDir) {
			_ = os.RemoveAll(filepath.Dir(sock))
		}
	})
	return f
}

func (f chatFixture) wantHostArgv(id domain.SessionID) []string {
	logPath, sock := sessionhost.Paths(f.dataDir, string(id))
	prompt := filepath.Join(promptfile.Dir(f.dataDir, id), promptfile.InitialPrompt)
	return []string{daemonExe, "session-host", "--log", logPath, "--socket", sock, "--prompt-file", prompt, "--", "launch"}
}

func (f chatFixture) assertHostLaunch(t *testing.T, id domain.SessionID, what string) {
	t.Helper()
	if got, want := f.rt.lastCfg.Argv, f.wantHostArgv(id); !slices.Equal(got, want) {
		t.Fatalf("%s argv = %q\nwant %q", what, got, want)
	}
	if f.rt.lastCfg.StdinFile != "" {
		t.Fatalf("%s stdin file = %q, want none: the host owns claude's stdin", what, f.rt.lastCfg.StdinFile)
	}
	if f.agent.lastLaunch.Prompt != "" {
		t.Fatalf("%s handed the agent the prompt %q, want it only in the host's prompt file", what, f.agent.lastLaunch.Prompt)
	}
	data, err := os.ReadFile(filepath.Join(promptfile.Dir(f.dataDir, id), promptfile.InitialPrompt)) //nolint:gosec // test-owned temp dir
	if err != nil || !strings.Contains(string(data), "do it") {
		t.Fatalf("%s prompt file = %q, %v; want the task prompt", what, data, err)
	}
}

func TestChatSpawnLaunchesTheSessionHost(t *testing.T) {
	f := newChatFixture(t)
	rec, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it", Transport: domain.TransportChat})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Transport != domain.TransportChat {
		t.Fatalf("stored transport = %q, want chat", rec.Transport)
	}
	f.assertHostLaunch(t, rec.ID, "spawn")
	if runtime.GOOS == "windows" {
		return
	}
	logPath, _ := sessionhost.Paths(f.dataDir, string(rec.ID))
	if info, err := os.Stat(filepath.Dir(logPath)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("log dir = %v, %v; want it created 0700", info, err)
	}
}

func TestChatRestoreAndRestartRelaunchTheHostNotAResume(t *testing.T) {
	f := newChatFixture(t)
	rec, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it", Transport: domain.TransportChat})
	if err != nil {
		t.Fatal(err)
	}

	row := f.st.sessions[rec.ID]
	row.IsTerminated = true
	row.Metadata.AgentSessionID = "agent-x"
	f.st.sessions[rec.ID] = row
	f.rt.lastCfg, f.agent.lastLaunch, f.agent.lastRestore = ports.RuntimeConfig{}, ports.LaunchConfig{}, ports.RestoreConfig{}
	if _, err := f.m.Restore(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	f.assertHostLaunch(t, rec.ID, "restore")
	if f.agent.lastRestore.Session.ID != "" {
		t.Fatal("restore asked the agent for a resume command; the host resumes from its own log")
	}

	f.rt.lastCfg, f.agent.lastLaunch = ports.RuntimeConfig{}, ports.LaunchConfig{}
	destroyed := f.rt.destroyed
	if _, err := f.m.Restart(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if f.rt.destroyed != destroyed+1 {
		t.Fatal("Restart did not recycle the live runtime in place")
	}
	f.assertHostLaunch(t, rec.ID, "restart")
	if f.agent.lastRestore.Session.ID != "" {
		t.Fatal("restart asked the agent for a resume command; the host resumes from its own log")
	}
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

func TestChatTodoStartsTheSessionHost(t *testing.T) {
	f := newChatFixture(t)
	todo, err := f.m.PrepareTodo(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it", Transport: domain.TransportChat})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.StartTodo(ctx, todo.ID); err != nil {
		t.Fatal(err)
	}
	f.assertHostLaunch(t, todo.ID, "todo start")
}
