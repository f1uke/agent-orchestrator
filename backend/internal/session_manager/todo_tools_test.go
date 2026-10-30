package sessionmanager

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const todoToolsEnv = "CLAUDE_CODE_ENABLE_TODO_TOOLS"

// stubAgentBinaries puts no-op claude and codex binaries on PATH and clears the
// user's own task-tools setting for the test.
func stubAgentBinaries(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub agent binaries are POSIX shell scripts")
	}
	bin := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // G306: a stub binary must be executable
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv(todoToolsEnv, "")
	if err := os.Unsetenv(todoToolsEnv); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeLaunchesCarryTheTaskToolsOnEveryTransportAndKind(t *testing.T) {
	stubAgentBinaries(t)
	for _, tc := range []struct {
		name      string
		kind      domain.SessionKind
		transport domain.Transport
	}{
		{"terminal worker", domain.KindWorker, domain.TransportTerminal},
		{"chat worker", domain.KindWorker, domain.TransportChat},
		{"orchestrator", domain.KindOrchestrator, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newChatFixture(t)
			f.m.agents = singleAgent{agent: claudecode.New()}
			rec, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: tc.kind, Prompt: "do it", Transport: tc.transport})
			if err != nil {
				t.Fatal(err)
			}
			if tc.transport != "" && rec.Transport != tc.transport {
				t.Fatalf("transport = %q, want %q", rec.Transport, tc.transport)
			}
			if got := f.rt.lastCfg.Env[todoToolsEnv]; got != "1" {
				t.Fatalf("runtime env %s = %q, want 1", todoToolsEnv, got)
			}
		})
	}
}

func TestOtherAgentsLaunchWithoutTheTaskToolsSetting(t *testing.T) {
	stubAgentBinaries(t)
	f := newChatFixture(t)
	f.m.agents = singleAgent{agent: codex.New()}
	cfg := testRoleAgents()
	cfg.Worker.Harness = domain.HarnessCodex
	f.st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}

	if _, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it"}); err != nil {
		t.Fatal(err)
	}
	if v, ok := f.rt.lastCfg.Env[todoToolsEnv]; ok {
		t.Fatalf("codex runtime env carries %s=%q", todoToolsEnv, v)
	}
}
