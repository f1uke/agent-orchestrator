package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// sendServer wires an httptest server expecting POST /api/v1/sessions/{id}/send
// and captures the request body and path the CLI hit.
type sendCapture struct {
	body string
	path string
}

// writeRunFileFor points the CLI's run-file at srv so postJSON dials the test
// server. It mirrors the run-file convention the other CLI tests use.
func writeRunFileFor(t *testing.T, cfg testConfig, srv *httptest.Server) {
	t.Helper()
	if err := runfile.Write(cfg.runFile, runfile.Info{
		PID: os.Getpid(), Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("write run-file: %v", err)
	}
}

func sendServer(t *testing.T, status int, respBody string) (*httptest.Server, *sendCapture) {
	t.Helper()
	capture := &sendCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/v1/sessions/") || !strings.HasSuffix(r.URL.Path, "/send") {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		capture.body = string(body)
		capture.path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

func TestSend_Success(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv, capture := sendServer(t, http.StatusOK,
		`{"ok":true,"sessionId":"demo-1","message":"hello agent"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "send", "--session", "demo-1", "--message", "hello agent")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/sessions/demo-1/send" {
		t.Errorf("path = %q, want /api/v1/sessions/demo-1/send", capture.path)
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	if req.Message != "hello agent" {
		t.Errorf("captured message = %q, want %q", req.Message, "hello agent")
	}
}

func TestSend_PrefixesMessageWithSenderSessionID(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := sendServer(t, http.StatusOK,
		`{"ok":true,"sessionId":"demo-1","message":"hi"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "send", "--session", "demo-1", "--message", "  hi  ")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	want := "[from @aa-47]   hi  "
	if req.Message != want {
		t.Errorf("captured message = %q, want %q", req.Message, want)
	}
}

func TestSend_BlankSenderSessionIDDoesNotPrefixMessage(t *testing.T) {
	t.Setenv("AO_SESSION_ID", " \t ")
	cfg := setConfigEnv(t)
	srv, capture := sendServer(t, http.StatusOK,
		`{"ok":true,"sessionId":"demo-1","message":"hello agent"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "send", "--session", "demo-1", "--message", "hello agent")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	if req.Message != "hello agent" {
		t.Errorf("captured message = %q, want %q", req.Message, "hello agent")
	}
}

func TestSend_PreservesMessageWhitespace(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv, capture := sendServer(t, http.StatusOK, `{"ok":true,"sessionId":"demo-1","message":"hi"}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "send", "--session", "demo-1", "--message", "  hi  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	if req.Message != "  hi  " {
		t.Errorf("server received %q, want preserved whitespace", req.Message)
	}
}

func TestSend_EmptyMessageIsUsageError(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "send", "--session", "demo-1", "--message", "   ")
	if err == nil {
		t.Fatal("expected usage error for empty message")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "--message is required") {
		t.Fatalf("error missing usage message: %v", err)
	}
}

// TestSend_AcceptsLargeMessage asserts the CLI forwards a full brief verbatim.
// The daemon's cap is now 128 KiB, so a report no longer has to be split into
// "1/3, 2/3, 3/3" by hand.
func TestSend_AcceptsLargeMessage(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv, capture := sendServer(t, http.StatusOK, `{"ok":true,"sessionId":"demo-1","message":"ok"}`)
	writeRunFileFor(t, cfg, srv)

	message := strings.Repeat("รายงานความคืบหน้า ", 1000) // ~52 KB of Thai
	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "send", "--session", "demo-1", "--message", message)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if req.Message != message {
		t.Fatalf("forwarded %d bytes, want the full %d verbatim", len(req.Message), len(message))
	}
}

// TestSend_MessageFileReadsFile asserts `--message-file <path>` loads the
// message from a file, mirroring `spawn --prompt-file`. A 128 KiB message on the
// command line is awkward (shell quoting, ARG_MAX); a file sidesteps both.
func TestSend_MessageFileReadsFile(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv, capture := sendServer(t, http.StatusOK, `{"ok":true,"sessionId":"demo-1","message":"ok"}`)
	writeRunFileFor(t, cfg, srv)

	message := strings.Repeat("รายงานฉบับเต็ม บรรทัดที่หนึ่ง\n", 800) // ~70 KB of Thai
	path := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(path, []byte(message), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "send", "--session", "demo-1", "--message-file", path)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if req.Message != message {
		t.Fatalf("forwarded %d bytes, want the file's %d verbatim", len(req.Message), len(message))
	}
}

// TestSend_MessageFileFromStdinKeepsSenderPrefix asserts `--message-file -`
// reads stdin and that the `[from @id]` tag is still applied, so a piped report
// stays attributable to its sender.
func TestSend_MessageFileFromStdinKeepsSenderPrefix(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := sendServer(t, http.StatusOK, `{"ok":true,"sessionId":"demo-1","message":"ok"}`)
	writeRunFileFor(t, cfg, srv)

	message := "piped report\nwith two lines\n"
	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true }, In: strings.NewReader(message),
	}, "send", "--session", "demo-1", "--message-file", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if want := "[from @aa-47] " + message; req.Message != want {
		t.Fatalf("message = %q, want %q", req.Message, want)
	}
}

// TestSend_MessageAndMessageFileMutuallyExclusive asserts passing both is a
// usage error (exit 2) that never reaches the daemon, matching spawn.
func TestSend_MessageAndMessageFileMutuallyExclusive(t *testing.T) {
	setConfigEnv(t)
	path := filepath.Join(t.TempDir(), "m.md")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCLI(t, Deps{}, "send", "--session", "demo-1", "--message", "inline", "--message-file", path)
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err=%v exit=%d, want a mutually-exclusive usage error", err, ExitCode(err))
	}
}

// TestSend_ChecksSessionBeforeReadingStdinMessage asserts the cheap flag checks
// run before `--message-file -` blocks on stdin, so a missing --session exits
// immediately instead of hanging an interactive invocation.
func TestSend_ChecksSessionBeforeReadingStdinMessage(t *testing.T) {
	setConfigEnv(t)
	deps := Deps{In: iotest.ErrReader(errors.New("stdin-sentinel-read"))}
	_, _, err := executeCLI(t, deps, "send", "--message-file", "-")
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "--session") {
		t.Fatalf("err=%v exit=%d, want a --session usage error before stdin is read", err, ExitCode(err))
	}
}

// TestSend_MessageFileMissing asserts a bad path fails clearly before any daemon
// round-trip.
func TestSend_MessageFileMissing(t *testing.T) {
	setConfigEnv(t)
	missing := filepath.Join(t.TempDir(), "nope.md")
	_, _, err := executeCLI(t, Deps{}, "send", "--session", "demo-1", "--message-file", missing)
	if err == nil || !strings.Contains(err.Error(), "message file") {
		t.Fatalf("err=%v, want a read error mentioning the message file", err)
	}
}

// TestSend_MessageFileEmpty asserts an empty file is rejected rather than
// silently sending a bare Enter to the agent.
func TestSend_MessageFileEmpty(t *testing.T) {
	setConfigEnv(t)
	path := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(path, []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCLI(t, Deps{}, "send", "--session", "demo-1", "--message-file", path)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d, want a usage error for an empty message file", err, ExitCode(err))
	}
}

func TestSend_MissingSessionIsUsageError(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "send", "--message", "hi")
	if err == nil {
		t.Fatal("expected usage error for missing --session")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestSend_ServerBadRequestExits1(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sendServer(t, http.StatusBadRequest,
		`{"error":"bad_request","code":"MESSAGE_REQUIRED","message":"Message is required"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "send", "--session", "demo-1", "--message", "hi")
	if err == nil {
		t.Fatal("expected runtime error from 400")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), "MESSAGE_REQUIRED") && !strings.Contains(errOut, "MESSAGE_REQUIRED") {
		t.Fatalf("error did not surface the server error envelope: %v\nstderr=%s", err, errOut)
	}
}

func TestSend_ServerNotFoundExits1(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sendServer(t, http.StatusNotFound,
		`{"error":"not_found","code":"SESSION_NOT_FOUND","message":"Unknown session"}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "send", "--session", "missing", "--message", "hi")
	if err == nil {
		t.Fatal("expected runtime error from 404")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
}

func TestSend_ServerInternalErrorExits1(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sendServer(t, http.StatusInternalServerError,
		`{"error":"internal","code":"SESSION_OPERATION_FAILED","message":"Session operation failed"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "send", "--session", "demo-1", "--message", "hi")
	if err == nil {
		t.Fatal("expected runtime error from 500")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	// Regression guard: a future change that swallows the API envelope and
	// prints only "daemon returned HTTP 500" would silently hide what the
	// daemon was trying to tell the operator.
	if !strings.Contains(err.Error(), "SESSION_OPERATION_FAILED") && !strings.Contains(errOut, "SESSION_OPERATION_FAILED") {
		t.Fatalf("error did not surface the server error envelope: %v\nstderr=%s", err, errOut)
	}
}

func TestSend_DaemonNotRunningExits1(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "send", "--session", "demo-1", "--message", "hi")
	if err == nil {
		t.Fatal("expected error when daemon is not running")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
}

func TestSend_NetworkErrorExits1(t *testing.T) {
	cfg := setConfigEnv(t)
	// Start and immediately close a server so the run-file points at a closed port.
	srv, _ := sendServer(t, http.StatusOK, "{}")
	writeRunFileFor(t, cfg, srv)
	srv.Close()

	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "send", "--session", "demo-1", "--message", "hi")
	if err == nil {
		t.Fatal("expected runtime error from network failure")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
}
