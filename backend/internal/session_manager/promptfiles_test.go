package sessionmanager

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/promptfile"
)

func managerWithAgentAndDataDir(t *testing.T, agents ports.AgentResolver) (*Manager, *fakeStore, *fakeRuntime, string) {
	t.Helper()
	dataDir := t.TempDir()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{
		Runtime: rt, Agents: agents, Workspace: &fakeWorkspace{path: t.TempDir()},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		DataDir: dataDir, LookPath: lookPath,
	})
	return m, st, rt, dataDir
}

// assertPrivatePromptFile checks the manager handed the agent a 0600 file under
// the session's prompt dir holding exactly the system prompt it built.
func assertPrivatePromptFile(t *testing.T, dataDir string, id domain.SessionID, path, systemPrompt string) {
	t.Helper()
	if systemPrompt == "" {
		t.Fatal("manager built no system prompt; nothing to check")
	}
	if want := filepath.Join(promptfile.Dir(dataDir, id), promptfile.SystemPrompt); path != want {
		t.Fatalf("system prompt file = %q, want %q", path, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("system prompt file: %v", err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("system prompt file mode = %o, want 600", got)
	}
	if data, _ := os.ReadFile(path); string(data) != systemPrompt { //nolint:gosec // test-owned temp dir
		t.Fatal("system prompt file does not hold the system prompt the manager built")
	}
}

// Spawn, resume and the fresh relaunch a restore falls back to each hand the
// agent its standing instructions as a private file, and an ending removes it.
func TestSystemPromptIsHandedToTheAgentByFile(t *testing.T) {
	agent := &recordingAgent{}
	m, st, _, dataDir := managerWithAgentAndDataDir(t, singleAgent{agent: agent})

	rec, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	assertPrivatePromptFile(t, dataDir, rec.ID, agent.lastLaunch.SystemPromptFile, agent.lastLaunch.SystemPrompt)

	// Resume: the agent can continue its native session.
	row := st.sessions[rec.ID]
	row.IsTerminated = true
	row.Metadata.AgentSessionID = "agent-x"
	st.sessions[rec.ID] = row
	if err := promptfile.Remove(dataDir, rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Restore(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	assertPrivatePromptFile(t, dataDir, rec.ID, agent.lastRestore.SystemPromptFile, agent.lastRestore.SystemPrompt)

	// Fresh relaunch: no native session to resume, so the prompt is replayed.
	row = st.sessions[rec.ID]
	row.IsTerminated = true
	row.Metadata.AgentSessionID = ""
	st.sessions[rec.ID] = row
	agent.lastLaunch = ports.LaunchConfig{}
	if _, err := m.Restore(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	assertPrivatePromptFile(t, dataDir, rec.ID, agent.lastLaunch.SystemPromptFile, agent.lastLaunch.SystemPrompt)

	if err := m.ReapSessionPanes(ctx, rec.ID); err != nil {
		t.Fatalf("ReapSessionPanes: %v", err)
	}
	if _, err := os.Stat(promptfile.Dir(dataDir, rec.ID)); !os.IsNotExist(err) {
		t.Fatalf("an ended session's prompt files survived the reap: %v", err)
	}
}

// End to end through the real Claude Code adapter: what reaches the runtime -
// the argv tmux will run - names the file and carries none of the text.
func TestSpawnedClaudeArgvCarriesNoSystemPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub claude binary is a POSIX shell script")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // G306: a stub binary must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	m, st, rt, dataDir := managerWithAgentAndDataDir(t, singleAgent{agent: claudecode.New()})
	st.num = 1
	// A live orchestrator puts its coordination block in the worker's prompt.
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}

	rec, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(promptfile.Dir(dataDir, rec.ID), promptfile.SystemPrompt)
	data, err := os.ReadFile(path) //nolint:gosec // test-owned temp dir
	if err != nil {
		t.Fatalf("system prompt file: %v", err)
	}
	systemPrompt := string(data)
	if !strings.Contains(systemPrompt, "## Orchestrator coordination") {
		t.Fatalf("system prompt file lacks the coordination block:\n%s", systemPrompt)
	}

	argv := rt.lastCfg.Argv
	if !containsPair(argv, "--append-system-prompt-file", path) {
		t.Fatalf("argv does not hand claude the prompt file:\n%q", argv)
	}
	for _, arg := range argv {
		if arg == "--append-system-prompt" || strings.Contains(arg, "## Orchestrator coordination") {
			t.Fatalf("argv carries the system prompt text:\n%q", argv)
		}
	}
}

func containsPair(argv []string, flag, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}
	return false
}

// The boot sweep removes the files of sessions that ended (or vanished) while
// the daemon was down, and leaves a live session's alone.
func TestReapOrphanedPromptFiles(t *testing.T) {
	m, st, _, dataDir := managerWithAgentAndDataDir(t, fakeAgents{})
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer", IsTerminated: true}
	for _, id := range []domain.SessionID{"mer-1", "mer-2", "mer-3"} {
		if _, err := promptfile.Write(dataDir, id, promptfile.SystemPrompt, "x"); err != nil {
			t.Fatal(err)
		}
	}

	reaped, err := m.ReapOrphanedPromptFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reaped != 2 {
		t.Fatalf("reaped = %d, want 2 (the ended and the vanished session)", reaped)
	}
	owners, _ := promptfile.Owners(dataDir)
	if len(owners) != 1 || owners[0] != "mer-1" {
		t.Fatalf("left = %v, want only the live session mer-1", owners)
	}
}
