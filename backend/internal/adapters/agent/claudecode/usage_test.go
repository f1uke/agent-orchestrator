package claudecode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// transcript is a synthetic Claude Code JSONL body. It exercises the load-bearing
// behaviors: one message split across three content-block lines (must count ONCE),
// a single-line message, a user line + a usage-less assistant line + a malformed
// line (all ignored), and a message with no id that dedupes on its uuid.
const transcript = `{"type":"user","message":{"role":"user","content":"hi"}}
{"type":"assistant","uuid":"a1","message":{"id":"msg_A","role":"assistant","content":[{"type":"thinking"}],"usage":{"input_tokens":100,"cache_creation_input_tokens":10,"cache_read_input_tokens":1000,"output_tokens":5}}}
{"type":"assistant","uuid":"a2","message":{"id":"msg_A","role":"assistant","content":[{"type":"text"}],"usage":{"input_tokens":100,"cache_creation_input_tokens":10,"cache_read_input_tokens":1000,"output_tokens":5}}}
{"type":"assistant","uuid":"a3","message":{"id":"msg_A","role":"assistant","content":[{"type":"tool_use"}],"usage":{"input_tokens":100,"cache_creation_input_tokens":10,"cache_read_input_tokens":1000,"output_tokens":5}}}
{"type":"assistant","uuid":"b1","message":{"id":"msg_B","role":"assistant","usage":{"input_tokens":200,"cache_creation_input_tokens":20,"cache_read_input_tokens":2000,"output_tokens":50}}}
{"type":"assistant","uuid":"noid1","message":{"id":"","role":"assistant","usage":{"input_tokens":1,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"output_tokens":4}}}
{"type":"assistant","uuid":"nousage","message":{"id":"msg_C","role":"assistant"}}
{not valid json
`

// wantUsage is the hand-computed dedup total of transcript: msg_A once, msg_B once,
// the id-less line once (via its uuid). turns = 3.
var wantUsage = domain.TokenUsage{Input: 301, CacheCreation: 32, CacheRead: 3003, Output: 59, Turns: 3}

// seedTranscript writes body into a temp Claude Code projects tree keyed on
// workspace + native session id, and points CLAUDE_CONFIG_DIR at it. It returns the
// workspace path to stamp on the session record.
func seedTranscript(t *testing.T, nativeID, body string) string {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	workspace := t.TempDir()
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	dir := filepath.Join(cfg, "projects", claudeProjectDirName(resolved))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, nativeID+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return workspace
}

func claudeRec(workspace, nativeID string) domain.SessionRecord {
	return domain.SessionRecord{
		ID:      "ses-1",
		Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{
			WorkspacePath:  workspace,
			AgentSessionID: nativeID,
		},
	}
}

func TestReadSessionUsage_DedupesByMessageID(t *testing.T) {
	workspace := seedTranscript(t, "native-1", transcript)
	got, ok, err := ReadSessionUsage(claudeRec(workspace, "native-1"))
	if err != nil {
		t.Fatalf("ReadSessionUsage: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true (a transcript exists)")
	}
	if got != wantUsage {
		t.Fatalf("usage = %+v, want %+v", got, wantUsage)
	}
	if got.RawTotal() != 3395 {
		t.Fatalf("RawTotal = %d, want 3395", got.RawTotal())
	}
}

func TestReadSessionUsage_NonClaudeIsNotOK(t *testing.T) {
	workspace := seedTranscript(t, "native-1", transcript)
	rec := claudeRec(workspace, "native-1")
	rec.Harness = domain.HarnessCodex
	_, ok, err := ReadSessionUsage(rec)
	if err != nil {
		t.Fatalf("ReadSessionUsage: %v", err)
	}
	if ok {
		t.Fatal("ok = true for a non-claude harness, want false (graceful n/a)")
	}
}

func TestReadSessionUsage_NoTranscriptIsNotOK(t *testing.T) {
	// Seed the projects tree but ask for a session whose transcript was never written.
	workspace := seedTranscript(t, "native-1", transcript)
	rec := claudeRec(workspace, "does-not-exist")
	rec.ID = "ses-unpinned" // its derived UUID transcript also does not exist
	_, ok, err := ReadSessionUsage(rec)
	if err != nil {
		t.Fatalf("ReadSessionUsage: %v", err)
	}
	if ok {
		t.Fatal("ok = true with no transcript on disk, want false")
	}
}

func TestReadSessionUsage_EmptyWorkspaceIsNotOK(t *testing.T) {
	rec := claudeRec("", "native-1")
	_, ok, err := ReadSessionUsage(rec)
	if err != nil {
		t.Fatalf("ReadSessionUsage: %v", err)
	}
	if ok {
		t.Fatal("ok = true with no workspace path, want false")
	}
}

func TestTranscriptPaths_DedupesAndFiltersToExisting(t *testing.T) {
	workspace := seedTranscript(t, "native-1", transcript)
	// agentSessionID resolves to the existing native-1 transcript; the derived id
	// for aoSessionID has no file, so only the existing one is returned.
	paths := TranscriptPaths(workspace, "ses-1", "native-1")
	if len(paths) != 1 {
		t.Fatalf("paths = %v, want exactly the one existing transcript", paths)
	}
	if filepath.Base(paths[0]) != "native-1.jsonl" {
		t.Fatalf("path = %s, want native-1.jsonl", paths[0])
	}

	// Identical agent/derived ids must not double-list the same file.
	same := claudeSessionUUID("ses-1")
	ws2 := seedTranscript(t, same, transcript)
	paths2 := TranscriptPaths(ws2, "ses-1", same)
	if len(paths2) != 1 {
		t.Fatalf("paths2 = %v, want 1 (deduped identical ids)", paths2)
	}
}
