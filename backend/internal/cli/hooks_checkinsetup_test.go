package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A transcript of a dev that read code and is about to post its check-in with
// no playbook read and no todolist.
func bareCheckInTranscript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lines := `{"type":"user","message":{"content":"/pstack:poteto-mode\n\nbuild the thing"}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"grep -rn parse src"}}]}}
`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func stopPayload(t *testing.T) string {
	b, _ := json.Marshal(map[string]any{"transcript_path": bareCheckInTranscript(t)})
	return string(b)
}

func TestHooks_StopRefusesAGatedCheckInWithoutSetUp(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	t.Setenv("AO_SESSION_KIND", "worker")
	t.Setenv(envCheckInSetup, "1")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{In: strings.NewReader(stopPayload(t)), ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", "stop")
	if err != nil {
		t.Fatal(err)
	}
	var block stopBlock
	if json.Unmarshal([]byte(out), &block) != nil || block.Decision != "block" || !strings.Contains(block.Reason, "no playbook file has been read") {
		t.Fatalf("stdout = %q, want a Stop block naming what is missing", out)
	}
	if capture.body != "" {
		t.Fatalf("a refused Stop must not report idle, posted %s", capture.body)
	}
}

// Without the flag (a qa, mechanical, ungated project, no pstack) the same
// Stop ends the turn and reports idle, exactly as before.
func TestHooks_StopIsNeverRefusedWithoutTheFlag(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	t.Setenv("AO_SESSION_KIND", "worker")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{In: strings.NewReader(stopPayload(t)), ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", "stop")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want nothing", out)
	}
	if got := capturedState(t, capture); got != "idle" {
		t.Fatalf("state = %q, want idle", got)
	}
}
