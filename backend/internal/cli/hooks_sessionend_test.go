package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func capturedEndReason(t *testing.T, capture *activityCapture) string {
	t.Helper()
	var req struct {
		End *struct {
			Reason string `json:"reason"`
		} `json:"end"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	if req.End == nil {
		return ""
	}
	return req.End.Reason
}

// The incident this exists for: a worker stopped mid-task and the only record
// was "exited". Claude Code had told the hook process exactly why, and the hook
// did not pass it on. It must ride the same post as the exited state.
func TestHooks_SessionEndCarriesTheHarnessReason(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	payload := `{"session_id":"native-1","transcript_path":"/Users/someone/.claude/projects/p/t.jsonl","hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`
	if _, _, err := executeCLI(t, Deps{In: strings.NewReader(payload), ProcessAlive: func(int) bool { return true }},
		"hooks", "claude-code", "session-end"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := capturedState(t, capture); got != "exited" {
		t.Fatalf("state = %q, want exited", got)
	}
	if got := capturedEndReason(t, capture); got != "prompt_input_exit" {
		t.Errorf("end.reason = %q, want the harness's reason", got)
	}
	// The reason is the ONLY thing lifted out of the ending payload. The rest of
	// it — the transcript path above included — must not cross the boundary.
	for _, forbidden := range []string{"/Users/someone", "transcript_path", "native-1"} {
		if strings.Contains(capture.body, forbidden) {
			t.Errorf("posted body contains %q, which must never leave the hook process:\n%s", forbidden, capture.body)
		}
	}
}

// An ending with no reason still reports the exit: dropping the post because the
// harness said nothing would lose the termination itself.
func TestHooks_SessionEndWithoutAReasonStillReportsTheExit(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	if _, _, err := executeCLI(t, Deps{In: strings.NewReader(`{"hook_event_name":"SessionEnd"}`), ProcessAlive: func(int) bool { return true }},
		"hooks", "claude-code", "session-end"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := capturedState(t, capture); got != "exited" {
		t.Fatalf("state = %q, want exited", got)
	}
	if got := capturedEndReason(t, capture); got != "" {
		t.Errorf("end.reason = %q, want empty when the harness reports none", got)
	}
}

// A non-terminal hook must not carry an end reason: an ending is a fact about
// the session stopping, not a field that tags along on every callback.
func TestHooks_NonTerminalHookCarriesNoEndReason(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	if _, _, err := executeCLI(t, Deps{In: strings.NewReader(`{"reason":"other"}`), ProcessAlive: func(int) bool { return true }},
		"hooks", "claude-code", "stop"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(capture.body, `"end"`) {
		t.Errorf("a stop hook posted an end block:\n%s", capture.body)
	}
}
