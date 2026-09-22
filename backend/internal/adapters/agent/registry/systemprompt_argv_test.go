package registry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// systemPromptCanary is standing-instruction text no argv may carry. It holds
// the words that did the damage on 2026-09-22: a worker ran
// `pkill -f 'xcodebuild test'` and killed every iOS agent on the machine,
// because the simulator guidance in their system prompt, passed as
// `--append-system-prompt "<text>"`, put those words on each agent's command
// line - which is exactly what `pkill -f` matches.
const systemPromptCanary = "AO-SYSTEM-PROMPT-CANARY: never run xcodebuild test from here"

// systemPromptOnArgv lists the harnesses whose CLI has no way to take
// APPENDED standing instructions from a file, so the text still rides on
// their command line. Each entry is a known exposure, not a pass: an agent of
// that harness stays killable by a pattern kill on a word in its prompt. The
// test also fails when an entry stops leaking, so the list cannot go stale.
var systemPromptOnArgv = map[domain.AgentHarness]string{
	"codex": "developer_instructions takes text only; the one file key, model_instructions_file, REPLACES Codex's base instructions",
	"qwen":  "--append-system-prompt takes text only; QWEN_SYSTEM_MD replaces the whole prompt",
	"goose": "--system takes text only; GOOSE_SYSTEM_PROMPT_FILE_PATH replaces the whole prompt",
	"cline": "-s takes text only (and replaces the default prompt); the file forms are rules inside the worktree",
}

// TestLaunchCommandsKeepSystemPromptOffArgv is the guard for the incident
// above: every harness handed its standing instructions both as text and as a
// private file - as the session manager and the reviewer launcher do - must
// launch AND resume with the file, never the text. It runs against the real
// adapters with stub binaries on PATH, so a new harness is covered the day it
// is registered.
func TestLaunchCommandsKeepSystemPromptOffArgv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub binaries are POSIX shell scripts")
	}
	stubAgentBinaries(t)
	promptFile := filepath.Join(t.TempDir(), "system-prompt.md")
	if err := os.WriteFile(promptFile, []byte(systemPromptCanary), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, ha := range Harnessed() {
		t.Run(string(ha.Harness), func(t *testing.T) {
			if ha.Harness == "autohand" {
				t.Setenv("AUTOHAND_CONFIG", filepath.Join(t.TempDir(), "config.json"))
			}
			_, exempt := systemPromptOnArgv[ha.Harness]
			leaked := false

			for _, kind := range []domain.SessionKind{domain.KindWorker, domain.KindOrchestrator} {
				argv, err := ha.Agent.GetLaunchCommand(context.Background(), ports.LaunchConfig{
					SessionID:        "proj-1",
					WorkspacePath:    t.TempDir(),
					Kind:             kind,
					Prompt:           "do the task",
					SystemPrompt:     systemPromptCanary,
					SystemPromptFile: promptFile,
				})
				if errors.Is(err, ports.ErrAgentBinaryNotFound) {
					t.Fatalf("launch: %v - add the harness's binary name to stubAgentBinaries", err)
				}
				if err != nil {
					t.Fatalf("launch (%s): %v", kind, err)
				}
				leaked = checkArgv(t, "launch "+string(kind), argv, exempt) || leaked
			}

			argv, ok, err := ha.Agent.GetRestoreCommand(context.Background(), ports.RestoreConfig{
				Kind: domain.KindWorker,
				Session: ports.SessionRef{
					ID:       "proj-1",
					Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "0b5f1c4e-2d6a-4f7e-9a1b-3c8d2e6f4a10"},
				},
				SystemPrompt:     systemPromptCanary,
				SystemPromptFile: promptFile,
			})
			if err != nil {
				t.Fatalf("restore: %v", err)
			}
			if ok {
				leaked = checkArgv(t, "restore", argv, exempt) || leaked
			}

			if exempt && !leaked {
				t.Errorf("%s keeps its system prompt off argv now: drop it from systemPromptOnArgv", ha.Harness)
			}
		})
	}
}

// checkArgv reports whether argv carries the canary, failing the test when it
// does for a harness that is not a known exposure.
func checkArgv(t *testing.T, what string, argv []string, exempt bool) bool {
	t.Helper()
	for _, arg := range argv {
		if !strings.Contains(arg, systemPromptCanary) {
			continue
		}
		if !exempt {
			t.Errorf("%s argv carries the system prompt text; pass SystemPromptFile instead:\n%q", what, argv)
		}
		return true
	}
	return false
}

// stubAgentBinaries puts an executable stub for every harness's binary first on
// PATH (and HOME on an empty dir, so no real install is found instead), which
// lets the real adapters build their commands on a machine with none of the
// CLIs installed.
func stubAgentBinaries(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{
		"agy", "aider", "amp", "auggie", "autohand", "claude", "cline", "cn",
		"codex", "copilot", "crush", "cursor-agent", "devin", "droid", "goose",
		"grok", "kilocode", "kimi", "kiro-cli", "opencode", "pi", "qwen", "vibe",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // G306: a stub binary must be executable
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
}
