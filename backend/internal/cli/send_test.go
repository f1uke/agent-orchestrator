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

// A queued send exits 0, so the ONLY thing separating it from a delivered one is
// what the CLI prints. Silence here would tell an operator (or an agent reading
// the output) that the message landed.
func TestSend_SaysWhenTheMessageWasQueuedInsteadOfDelivered(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv, _ := sendServer(t, http.StatusOK,
		`{"ok":true,"sessionId":"demo-1","message":"hello","queued":true,"pendingMessages":2}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "send", "--session", "demo-1", "--message", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "queued for demo-1") {
		t.Fatalf("output = %q, want it to say the message was queued", out)
	}
	if !strings.Contains(out, "not listening") || !strings.Contains(out, "2 waiting") {
		t.Fatalf("output = %q, want the reason and how many are waiting", out)
	}
}

// A delivered send stays silent, as it always has.
func TestSend_SaysNothingWhenTheMessageWasDelivered(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv, _ := sendServer(t, http.StatusOK, `{"ok":true,"sessionId":"demo-1","message":"hello"}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "send", "--session", "demo-1", "--message", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("output = %q, want nothing for a delivered message", out)
	}
}

// ADDRESSING YOUR CREWMATE BY ROLE. dev cannot know qa's id: a crew is formed
// after dev's runtime is already launched, and a qa attached later arrives later
// still - so an id would be empty exactly when it mattered. The daemon resolves
// the role, and the request names the SENDER in its path because that is the one
// id a session always knows.
func TestSend_CrewRoleAddressesTheDaemonsResolution(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "demo-1")
	cfg := setConfigEnv(t)
	srv, capture := crewSendServer(t, http.StatusOK, `{"ok":true,"sessionId":"demo-2","message":"pushed"}`)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"send", "--crew", "qa", "--about", "4a1b2c3", "--message", "pushed the fix"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/sessions/demo-1/crew/send" {
		t.Errorf("path = %q, want the SENDER's crew/send", capture.path)
	}
	var req struct {
		Role    string `json:"role"`
		About   string `json:"about"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	if req.Role != "qa" || req.About != "4a1b2c3" {
		t.Errorf("body = role %q about %q, want qa/4a1b2c3", req.Role, req.About)
	}
	if !strings.Contains(req.Message, "pushed the fix") {
		t.Errorf("message = %q, want it to carry what was typed", req.Message)
	}
}

// The plain form carries the sender too, so a crew member that addresses its
// crewmate by ID is capped by exactly the same rules. Leaving that path uncapped
// would leave the one command both prompts used before wide open.
func TestSend_CarriesTheSenderAndSubjectOnThePlainForm(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "demo-1")
	cfg := setConfigEnv(t)
	srv, capture := sendServer(t, http.StatusOK, `{"ok":true,"sessionId":"demo-2","message":"hi"}`)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"send", "--session", "demo-2", "--about", "tab-stays-live", "--message", "recorded a pass"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var req struct {
		From  string `json:"from"`
		About string `json:"about"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	if req.From != "demo-1" || req.About != "tab-stays-live" {
		t.Errorf("body = from %q about %q, want demo-1/tab-stays-live", req.From, req.About)
	}
}

// --crew names your crewmate, so it cannot mean anything outside a session.
func TestSend_CrewRoleNeedsASession(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"send", "--crew", "qa", "--message", "hello")
	if err == nil {
		t.Fatal("--crew was accepted with no session behind it")
	}
	if !strings.Contains(err.Error(), "--session") {
		t.Errorf("error = %q, want it to point at --session", err)
	}
}

func TestSend_CrewAndSessionAreExclusive(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "demo-1")
	setConfigEnv(t)
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"send", "--crew", "qa", "--session", "demo-2", "--message", "hello"); err == nil {
		t.Fatal("--crew and --session were accepted together")
	}
}

// crewSendServer expects POST /api/v1/sessions/{id}/crew/send.
func crewSendServer(t *testing.T, status int, respBody string) (*httptest.Server, *sendCapture) {
	t.Helper()
	capture := &sendCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/crew/send") {
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

// WHAT QA SEES WHEN IT HANDS BACK OVER CASES NOBODY DROVE.
//
// The message has already gone by the time this prints, and that is the design:
// refusing the handback recreates the silent stall the handback obligation
// exists to prevent. So the CLI's job is to make the gap impossible to miss and
// to name the two things that close it.
func TestSend_HandbackNamesTheCasesNobodyDrove(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "demo-2")
	cfg := setConfigEnv(t)
	srv, _ := crewSendServer(t, http.StatusOK,
		`{"ok":true,"sessionId":"demo-1","message":"run done","handback":{"cases":4,"notDriven":["tab-stays-live","drag-scroll"]}}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"send", "--crew", "dev", "--about", "4a1b2c3", "--message", "run done")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{
		"2 of 4",               // the size of what was left
		"tab-stays-live",       // named, so the reader is not sent back to the list
		"drag-scroll",          //
		"ao smoke record",      // the way to drive one
		"--verdict skip",       // the way to declare one undriveable
		"come from an ATTEMPT", // and the rule that keeps that honest
		"--still-working",      // the way to say the run is not over
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the handback report does not say %q:\n%s", want, out)
		}
	}
}

// A complete handback needs no commentary. A gate that talks after finished work
// is one people learn to read past.
func TestSend_ACompleteHandbackSaysNothingExtra(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "demo-2")
	cfg := setConfigEnv(t)
	srv, _ := crewSendServer(t, http.StatusOK,
		`{"ok":true,"sessionId":"demo-1","message":"run done","handback":{"cases":4,"notDriven":[]}}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"send", "--crew", "dev", "--about", "4a1b2c3", "--message", "run done")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("a complete handback printed something:\n%s", out)
	}
}

// "I am not finished yet" travels to the daemon, because the daemon is what
// decides whether to look at the checklist.
func TestSend_StillWorkingIsCarriedAndNeedsACrewmate(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "demo-2")
	cfg := setConfigEnv(t)
	srv, capture := crewSendServer(t, http.StatusOK, `{"ok":true,"sessionId":"demo-1","message":"still going"}`)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"send", "--crew", "dev", "--about", "4a1b2c3", "--still-working", "--message", "still going"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var req struct {
		StillWorking bool `json:"stillWorking"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	if !req.StillWorking {
		t.Fatalf("--still-working did not reach the daemon: %s", capture.body)
	}

	// It is a claim about your own crew run, so it is meaningless without one.
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"send", "--session", "demo-1", "--still-working", "--message", "hello"); err == nil {
		t.Fatal("--still-working was accepted outside a crew send")
	}
}

// A worker's report to the orchestrator went out on work nobody checked, and the
// sender is told so on its own stdout.
//
// AO used to put a qa on a task by itself the first time it drove the app. That
// is gone - it fired while dev was still using the device - so dev asks, and this
// is what catches a dev that never did. It is NOT a refusal: the message is
// delivered and the CLI exits 0, because a report that never lands is worse than
// one with a warning attached to it.
func TestSend_SaysWhenTheReportWentOutUnchecked(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-orch","unreviewed":{"touch":"sim"}}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"send", "--session", "demo-orch", "--message", "done, PR is green")
	if err != nil {
		t.Fatalf("the report was refused: %v stderr=%s", err, errOut)
	}
	for _, want := range []string{"sent", "took the simulator", "no qa was ever on it", "ao crew review"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the sender was not told %q:\n%s", want, out)
		}
	}
}

// And it says nothing at all otherwise. A warning that prints on every message is
// one nobody reads, which is the whole reason the daemon answers this question
// rather than the CLI guessing at it.
func TestSend_SaysNothingWhenThereIsNothingToSay(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-orch"}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"send", "--session", "demo-orch", "--message", "backend only, nothing to drive")
	if err != nil {
		t.Fatalf("ao send: %v stderr=%s", err, errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("an ordinary send printed something:\n%s", out)
	}
}
