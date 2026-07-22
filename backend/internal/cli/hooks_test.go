package cli

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type activityCapture struct {
	body string
	path string
	hits int
}

// activityServer accepts POST /api/v1/sessions/{id}/activity and records what
// the CLI sent. It mirrors sendServer in send_test.go.
func activityServer(t *testing.T, status int, respBody string) (*httptest.Server, *activityCapture) {
	t.Helper()
	capture := &activityCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/activity") {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		capture.body = string(body)
		capture.path = r.URL.Path
		capture.hits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

func capturedState(t *testing.T, capture *activityCapture) string {
	t.Helper()
	var req struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	return req.State
}

func TestHooks_NotificationReportsWaitingInput(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true,"sessionId":"ao-7","state":"waiting_input"}`)
	writeRunFileFor(t, cfg, srv)

	// A permission_prompt genuinely blocks the agent on the human, so it reports
	// waiting_input. An idle_prompt does not (see TestHooks_IdlePromptIsNoOp).
	_, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"notification_type":"permission_prompt"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "notification")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/sessions/ao-7/activity" {
		t.Errorf("path = %q, want /api/v1/sessions/ao-7/activity", capture.path)
	}
	if got := capturedState(t, capture); got != "waiting_input" {
		t.Errorf("state = %q, want waiting_input", got)
	}
}

// A recap / auto-summary turn ends the turn and Claude Code emits an idle_prompt
// Notification while the session sits quiet. It is informational — the agent is
// not requesting input — so the hook reports nothing to the daemon and the
// session keeps whatever status its durable facts already imply (e.g. a
// ready-to-merge PR).
func TestHooks_IdlePromptIsNoOp(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"notification_type":"idle_prompt"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "notification")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.hits != 0 {
		t.Errorf("expected no daemon call for an informational idle_prompt, got %d", capture.hits)
	}
}

func TestHooks_SessionEndReportsExited(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"reason":"logout"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "session-end")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := capturedState(t, capture); got != "exited" {
		t.Errorf("state = %q, want exited", got)
	}
}

func TestHooks_StopReportsIdle(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "stop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := capturedState(t, capture); got != "idle" {
		t.Errorf("state = %q, want idle", got)
	}
}

func TestHooks_CodexPermissionRequestReportsWaitingInput(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"tool_name":"Bash"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "codex", "permission-request")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := capturedState(t, capture); got != "waiting_input" {
		t.Errorf("state = %q, want waiting_input", got)
	}
}

func TestHooks_OpenCodeUserPromptReportsActive(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"ses-1","prompt":"fix this"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "opencode", "user-prompt-submit")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := capturedState(t, capture); got != "active" {
		t.Errorf("state = %q, want active", got)
	}
}

func TestHooks_RejectsMalformedSessionID(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "../etc/passwd")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"reason":"logout"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "session-end")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.hits != 0 {
		t.Errorf("expected no daemon call for an out-of-alphabet session id, got %d", capture.hits)
	}
}

func TestHooks_NoSessionIDIsNoOp(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"notification_type":"permission_prompt"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "notification")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.hits != 0 {
		t.Errorf("expected no daemon call for a non-AO session, got %d", capture.hits)
	}
}

func TestHooks_UntrackedEventIsNoOp(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"notification_type":"auth_success"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "notification")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.hits != 0 {
		t.Errorf("expected no daemon call for an untracked notification, got %d", capture.hits)
	}
}

func TestHooks_DaemonDownIsBestEffort(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	setConfigEnv(t) // no run-file written: daemon is "not running"

	_, _, err := executeCLI(t, Deps{
		In: strings.NewReader(`{"reason":"logout"}`),
	}, "hooks", "claude-code", "session-end")
	if err != nil {
		t.Fatalf("hooks must be best-effort (exit 0) when the daemon is down, got: %v", err)
	}
}

// TestHooks_DeliveryFailureGoesToHooksLog covers the durable failure sink:
// agents swallow hook stderr, so a delivery failure must also land in
// $AO_DATA_DIR/hooks.log — and a delivered hook must not write the file at all.
func TestHooks_DeliveryFailureGoesToHooksLog(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantLog bool
		wantIn  []string
	}{
		{
			name:    "daemon error is appended",
			status:  http.StatusInternalServerError,
			body:    `{"error":"internal","code":"BOOM","message":"boom"}`,
			wantLog: true,
			wantIn:  []string{"ao hooks claude-code session-end", "session=ao-7"},
		},
		{
			name:   "successful delivery writes nothing",
			status: http.StatusOK,
			body:   `{"ok":true}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "ao-7")
			cfg := setConfigEnv(t)
			srv, _ := activityServer(t, tc.status, tc.body)
			writeRunFileFor(t, cfg, srv)

			_, _, err := executeCLI(t, Deps{
				In:           strings.NewReader(`{"reason":"logout"}`),
				ProcessAlive: func(int) bool { return true },
			}, "hooks", "claude-code", "session-end")
			if err != nil {
				t.Fatalf("hooks must exit 0, got: %v", err)
			}

			logPath := filepath.Join(cfg.dataDir, "hooks.log")
			data, err := os.ReadFile(logPath)
			if !tc.wantLog {
				if !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("hooks.log should not exist after a delivered hook, got err=%v data=%q", err, data)
				}
				return
			}
			if err != nil {
				t.Fatalf("hooks.log not written: %v", err)
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(string(data), want) {
					t.Errorf("hooks.log missing %q:\n%s", want, data)
				}
			}
		})
	}
}

// TestHooks_HooksLogTruncatesPastCap asserts the size guard: an append against
// a hooks.log already past the cap truncates it first, so a persistently
// failing hook cannot grow the file without bound.
func TestHooks_HooksLogTruncatesPastCap(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t) // no run file written: every delivery fails
	logPath := filepath.Join(cfg.dataDir, "hooks.log")
	if err := os.MkdirAll(cfg.dataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	oversized := strings.Repeat("x", maxHooksLogBytes+1)
	if err := os.WriteFile(logPath, []byte(oversized), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCLI(t, Deps{
		In: strings.NewReader(`{"reason":"logout"}`),
	}, "hooks", "claude-code", "session-end")
	if err != nil {
		t.Fatalf("hooks must exit 0, got: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > maxHooksLogBytes {
		t.Fatalf("hooks.log = %d bytes, want truncated below the %d cap", len(data), maxHooksLogBytes)
	}
	if !strings.Contains(string(data), "ao hooks claude-code session-end") {
		t.Errorf("truncated hooks.log missing the new failure line:\n%s", data)
	}
}

func TestHooks_DaemonErrorIsSwallowed(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, _ := activityServer(t, http.StatusInternalServerError,
		`{"error":"internal","code":"BOOM","message":"boom"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"reason":"logout"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "session-end")
	if err != nil {
		t.Fatalf("hooks must exit 0 even on a daemon error, got: %v", err)
	}
	if !strings.Contains(errOut, "ao hooks") {
		t.Errorf("expected the failure surfaced to stderr, got %q", errOut)
	}
}

func capturedDetail(t *testing.T, capture *activityCapture) map[string]any {
	t.Helper()
	var req struct {
		Detail map[string]any `json:"detail"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	return req.Detail
}

// The whole point of the feed: a tool hook must carry the curated detail to the
// daemon in the SAME post that reports the activity state, so the bubble learns
// what the agent is doing without a second round trip.
func TestHooks_ToolHookCarriesCuratedDetail(t *testing.T) {
	cases := []struct {
		event   string
		payload string
		want    map[string]any
	}{
		{
			event:   "pre-tool-use",
			payload: `{"tool_name":"Bash","tool_input":{"command":"pnpm test","description":"Running the test suite"}}`,
			want:    map[string]any{"kind": "tool_start", "tool": "Bash", "text": "Running the test suite"},
		},
		{
			event:   "post-tool-use",
			payload: `{"tool_name":"Read","tool_input":{"file_path":"/Users/someone/x/hooks.go"}}`,
			want:    map[string]any{"kind": "tool_end", "tool": "Read", "target": "hooks.go"},
		},
		{
			event:   "post-tool-use-failure",
			payload: `{"tool_name":"Bash","tool_input":{"command":"pnpm test","description":"Running the test suite"}}`,
			want:    map[string]any{"kind": "tool_failed", "tool": "Bash", "text": "Running the test suite"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.event, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "ao-7")
			cfg := setConfigEnv(t)
			srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
			writeRunFileFor(t, cfg, srv)

			_, _, err := executeCLI(t, Deps{
				In:           strings.NewReader(tc.payload),
				ProcessAlive: func(int) bool { return true },
			}, "hooks", "claude-code", tc.event)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := capturedState(t, capture); got != "active" {
				t.Errorf("state = %q, want active", got)
			}
			got := capturedDetail(t, capture)
			for k, want := range tc.want {
				if got[k] != want {
					t.Errorf("detail[%q] = %v, want %v (body=%s)", k, got[k], want, capture.body)
				}
			}
		})
	}
}

// TestHooks_RawPayloadNeverLeavesTheHookProcess is the end-to-end secret-leak
// guard. A Write's tool_input is the ENTIRE file body and the PostToolUse
// payload also carries tool_response; the curated POST must contain neither.
// Curation happens inside `ao hooks`, so the raw payload never crosses a process
// boundary at all.
//
// Mutation check: forward the raw payload (or widen the per-tool whitelist) and
// this goes red.
func TestHooks_RawPayloadNeverLeavesTheHookProcess(t *testing.T) {
	const secret = "CANARY-E2E-FILE-BODY-b17e"
	payload := `{
		"session_id":"native-1","transcript_path":"/Users/someone/.claude/projects/p/t.jsonl",
		"hook_event_name":"PostToolUse","tool_name":"Write",
		"tool_input":{"file_path":"/Users/someone/private/.env","content":"OPENAI_KEY=` + secret + `"},
		"tool_response":{"filePath":"/Users/someone/private/.env","content":"OPENAI_KEY=` + secret + `"}
	}`

	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(payload),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "post-tool-use")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.hits != 1 {
		t.Fatalf("hits = %d, want 1", capture.hits)
	}
	if strings.Contains(capture.body, secret) {
		t.Fatalf("SECRET LEAK: the file body reached the daemon:\n%s", capture.body)
	}
	for _, forbidden := range []string{"/Users/someone", "tool_response", "transcript_path", "OPENAI_KEY"} {
		if strings.Contains(capture.body, forbidden) {
			t.Errorf("posted body contains %q, which must never leave the hook process:\n%s", forbidden, capture.body)
		}
	}
	if got := capturedDetail(t, capture); got["target"] != ".env" || got["tool"] != "Write" {
		t.Errorf("detail = %v, want the base name only", got)
	}
}

// A tool AO does not curate still reports that something is happening, with no
// detail whatsoever — degrade by saying less, never by leaking.
func TestHooks_UncuratedToolPostsNoDetail(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"tool_name":"mcp__private__deploy","tool_input":{"description":"Deploying","token":"s3cr3t"}}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "pre-tool-use")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(capture.body, "s3cr3t") || strings.Contains(capture.body, "Deploying") {
		t.Fatalf("uncurated tool leaked its input:\n%s", capture.body)
	}
	got := capturedDetail(t, capture)
	if got["kind"] != "tool_start" {
		t.Errorf("kind = %v, want tool_start", got["kind"])
	}
	if got["tool"] != nil || got["target"] != nil || got["text"] != nil {
		t.Errorf("detail = %v, want no fields beyond the kind", got)
	}
}

// A harness with no per-tool hook (codex and ~9 others) reports its activity
// state and nothing else. No "unsupported" flag rides along.
func TestHooks_HooklessHarnessStaysStatusOnly(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"tool_name":"Bash"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "codex", "permission-request")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := capturedState(t, capture); got != "waiting_input" {
		t.Errorf("state = %q, want waiting_input", got)
	}
	if strings.Contains(capture.body, "detail") || strings.Contains(capture.body, "unsupported") {
		t.Errorf("a hook-less harness must post a bare state:\n%s", capture.body)
	}
}

// opencode's plugin ships tool events after the part-filter fix; they carry the
// tool NAME only.
func TestHooks_OpenCodeToolEventCarriesNameOnlyDetail(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"ses-1","tool":"bash"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "opencode", "tool-start")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := capturedState(t, capture); got != "active" {
		t.Errorf("state = %q, want active", got)
	}
	got := capturedDetail(t, capture)
	if got["kind"] != "tool_start" || got["tool"] != "Bash" {
		t.Errorf("detail = %v, want a name-only Bash tool_start", got)
	}
	if got["text"] != nil || got["target"] != nil {
		t.Errorf("opencode detail must be name-only, got %v", got)
	}
}
