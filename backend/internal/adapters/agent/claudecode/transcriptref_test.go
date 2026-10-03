package claudecode

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/learn/fingerprint"
)

func TestTranscriptRef(t *testing.T) {
	payload := []byte(`{"session_id":"4983f40a-5e7d-5092-ac33-9c6f9f318cff","transcript_path":"/h/.claude/projects/-x/4983f40a-5e7d-5092-ac33-9c6f9f318cff.jsonl","prompt":"no,  use the\nscript","cwd":"/x"}`)

	ref, ok := TranscriptRef("user-prompt-submit", payload)
	if !ok {
		t.Fatal("prompt submit reported no ref")
	}
	want, n := fingerprint.Of("no, use the script")
	if ref.PromptSHA256 != want || ref.PromptBytes != n {
		t.Errorf("prompt fingerprint = %s/%d, want %s/%d", ref.PromptSHA256, ref.PromptBytes, want, n)
	}
	if ref.ClaudeSessionID != "4983f40a-5e7d-5092-ac33-9c6f9f318cff" || !strings.HasSuffix(ref.TranscriptPath, ".jsonl") {
		t.Errorf("ref = %+v", ref)
	}

	// Only a prompt submit carries a prompt; the others report the file alone.
	ref, ok = TranscriptRef("stop", payload)
	if !ok || ref.PromptSHA256 != "" {
		t.Errorf("stop ref = %+v ok=%v", ref, ok)
	}
	// Tool hooks fire per tool call and must not post a ref each time.
	if _, ok := TranscriptRef("pre-tool-use", payload); ok {
		t.Error("a tool hook reported a ref")
	}
	if _, ok := TranscriptRef("session-start", []byte(`{"session_id":"x"}`)); ok {
		t.Error("an envelope with no transcript path reported a ref")
	}
	// A native id that is not id-shaped is dropped, the path still reported.
	ref, _ = TranscriptRef("stop", []byte(`{"session_id":"../../etc","transcript_path":"/a/b.jsonl"}`))
	if ref.ClaudeSessionID != "" {
		t.Errorf("a malformed native id crossed: %q", ref.ClaudeSessionID)
	}
}

func TestIsTranscriptPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	projects := filepath.Join(root, "projects")
	cases := map[string]bool{
		filepath.Join(projects, "-Users-x-wt", "abc.jsonl"):          true,
		filepath.Join(projects, "-Users-x-wt", "abc.json"):           false,
		filepath.Join(projects, "abc.jsonl"):                         false,
		filepath.Join(projects, "-Users-x-wt", "sub", "abc.jsonl"):   false,
		filepath.Join(projects, "..", "settings.jsonl"):              false,
		"relative/abc.jsonl":                                         false,
		filepath.Join(root, "elsewhere", "-Users-x-wt", "abc.jsonl"): false,
	}
	for p, want := range cases {
		if got := IsTranscriptPath(p); got != want {
			t.Errorf("IsTranscriptPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestPinnedTranscriptPath_IsTheLaunchID(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	p, err := PinnedTranscriptPath("/wt/proj/feat", "proj-7")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != claudeSessionUUID("proj-7")+".jsonl" || filepath.Base(filepath.Dir(p)) != "-wt-proj-feat" {
		t.Errorf("pinned path = %s", p)
	}
}
