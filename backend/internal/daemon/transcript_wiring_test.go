package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// seedTranscript writes a Claude Code transcript for workspace and returns its
// path. It reproduces the projects-dir mapping (every byte outside [A-Za-z0-9-]
// becomes '-') because that helper is unexported — and then VERIFIES the guess
// through claudecode.TranscriptPaths, so a drift in the real mapping fails the
// setup loudly instead of quietly making the assertions below vacuous.
func seedTranscript(t *testing.T, nativeID string) (workspace, path string) {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	workspace = t.TempDir()
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	name := strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, resolved)
	dir := filepath.Join(cfg, "projects", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path = filepath.Join(dir, nativeID+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	if got := claudecode.TranscriptPaths(workspace, "ao-1", nativeID); len(got) != 1 || got[0] != path {
		t.Fatalf("setup is stale: TranscriptPaths = %v, want [%s]", got, path)
	}
	return workspace, path
}

// A claude-code session's ending carries a pointer to the transcript that exists
// on disk — the only surviving account of what the agent was doing.
func TestLocateTranscript_ClaudeCodeReportsTheTranscriptOnDisk(t *testing.T) {
	workspace, want := seedTranscript(t, "native-1")
	rec := domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{WorkspacePath: workspace, AgentSessionID: "native-1"},
	}
	if got := locateTranscript(rec); got != want {
		t.Errorf("locateTranscript = %q, want %q", got, want)
	}
	if !filepath.IsAbs(want) {
		t.Errorf("transcript pointer %q must be absolute", want)
	}
}

// The same worktree can hold a Claude transcript from an earlier session while
// THIS session runs a different harness. Attaching that file to a codex ending
// would put another agent's account on this session's record.
func TestLocateTranscript_IgnoresAClaudeTranscriptForAnotherHarness(t *testing.T) {
	workspace, _ := seedTranscript(t, "native-1")
	rec := domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessCodex,
		Metadata: domain.SessionMetadata{WorkspacePath: workspace, AgentSessionID: "native-1"},
	}
	if got := locateTranscript(rec); got != "" {
		t.Errorf("locateTranscript = %q, want empty for a non-claude harness", got)
	}
}

// A harness whose transcript layout AO does not know must report NO pointer.
// Guessing a path for it would put a file reference on the record that resolves
// to nothing — worse than the honest silence, because a reader would go looking.
func TestLocateTranscript_OnlyForHarnessesAOCanRead(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessOpenCode, domain.HarnessAider, ""} {
		rec := domain.SessionRecord{
			ID: "ao-1", Harness: harness,
			Metadata: domain.SessionMetadata{WorkspacePath: filepath.Join(t.TempDir(), "ws"), AgentSessionID: "native-1"},
		}
		if got := locateTranscript(rec); got != "" {
			t.Errorf("locateTranscript(%q) = %q, want empty", harness, got)
		}
	}
}

// A claude-code session with no transcript on disk reports nothing rather than a
// path that does not exist: TranscriptPaths only returns files it stat'd.
func TestLocateTranscript_ClaudeCodeWithNoTranscriptOnDisk(t *testing.T) {
	rec := domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{WorkspacePath: filepath.Join(t.TempDir(), "never-used"), AgentSessionID: "native-1"},
	}
	if got := locateTranscript(rec); got != "" {
		t.Errorf("locateTranscript = %q, want empty when no transcript exists", got)
	}
}

// A session with no workspace recorded (a spawn that failed part-way) has
// nothing to derive a transcript path from, and must not produce a partial one.
func TestLocateTranscript_NoWorkspaceYieldsNoPointer(t *testing.T) {
	rec := domain.SessionRecord{ID: "ao-1", Harness: domain.HarnessClaudeCode}
	if got := locateTranscript(rec); got != "" {
		t.Errorf("locateTranscript = %q, want empty without a workspace", got)
	}
}
