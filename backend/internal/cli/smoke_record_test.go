package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// smokeRequest is one call the CLI made, kept in order so a command that has to
// do two things (upload, then record) can be checked as a sequence.
type smokeRequest struct {
	method string
	path   string
	query  string
	body   string
	ctype  string
	name   string
}

func smokeMultiServer(t *testing.T, respBody string) (*httptest.Server, *[]smokeRequest) {
	t.Helper()
	var seen []smokeRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// The CLI fires a telemetry ping on every invocation; it is not part of
		// what any of these commands is being tested for.
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		seen = append(seen, smokeRequest{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			body:   string(body),
			ctype:  r.Header.Get("Content-Type"),
			name:   r.Header.Get("X-Filename"),
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

const recordedCheck = `{"check":{"id":"a","seq":1,"name":"A fresh MR shows up","verdict":"pass","agentVerdict":"fail","agentSha":"abc123def4567"}}`

// TestSmokeRecordPostsTheAgentResult: the command writes the MACHINE's fields on
// their own endpoint, and its success line says out loud that the user's verdict
// is untouched - the one thing a reader must not misread.
func TestSmokeRecordPostsTheAgentResult(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, seen := smokeMultiServer(t, recordedCheck)
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.CommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "git" || strings.Join(args, " ") != "rev-parse HEAD" {
			t.Fatalf("unexpected command: %s %v", name, args)
		}
		return []byte("abc123def4567\n"), nil
	}

	out, errOut, err := executeCLI(t, deps, "smoke", "record", "w1", "--case", "a", "--verdict", "fail", "--note", "step 2 timed out")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if len(*seen) != 1 {
		t.Fatalf("requests = %+v, want one", *seen)
	}
	req := (*seen)[0]
	if req.method != http.MethodPost || req.path != "/api/v1/sessions/w1/smoke-checks/a/agent-result" {
		t.Fatalf("request = %s %s", req.method, req.path)
	}
	var body recordSmokeAgentResultRequest
	if err := json.Unmarshal([]byte(req.body), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Verdict != "fail" || body.Note != "step 2 timed out" {
		t.Fatalf("body = %+v", body)
	}
	// The commit defaults from the worktree, so a result can be told from a stale one.
	if body.SHA != "abc123def4567" {
		t.Fatalf("sha = %q, want HEAD of the checkout", body.SHA)
	}
	if !strings.Contains(out, "the user's verdict is unchanged") {
		t.Fatalf("output = %q", out)
	}
}

func TestSmokeRecordExplicitSHAWins(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, seen := smokeMultiServer(t, recordedCheck)
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.CommandOutput = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("git must not be consulted when --sha is given")
		return nil, nil
	}
	if _, errOut, err := executeCLI(t, deps, "smoke", "record", "w1", "--case", "a", "--verdict", "pass", "--sha", "0badc0de"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var body recordSmokeAgentResultRequest
	if err := json.Unmarshal([]byte((*seen)[0].body), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.SHA != "0badc0de" {
		t.Fatalf("sha = %q", body.SHA)
	}
}

// TestSmokeRecordUploadsEvidenceAsTheAgents: a captured file must reach the
// machine's own evidence list, or the two lists collapse into one indistinct pile
// and the provenance is worthless.
func TestSmokeRecordUploadsEvidenceAsTheAgents(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, seen := smokeMultiServer(t, recordedCheck)
	writeRunFileFor(t, cfg, srv)

	shot := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(shot, []byte("PNGDATA"), 0o600); err != nil {
		t.Fatalf("write evidence: %v", err)
	}

	deps := aliveDeps()
	deps.CommandOutput = func(context.Context, string, ...string) ([]byte, error) { return []byte("sha\n"), nil }
	if _, errOut, err := executeCLI(t, deps, "smoke", "record", "w1", "--case", "a", "--evidence", shot); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if len(*seen) != 2 {
		t.Fatalf("requests = %+v, want the upload then the record", *seen)
	}
	upload := (*seen)[0]
	if upload.path != "/api/v1/sessions/w1/smoke-checks/a/evidence" || upload.query != "source=agent" {
		t.Fatalf("upload = %s?%s, want the evidence route tagged agent", upload.path, upload.query)
	}
	if upload.ctype != "image/png" || upload.name != "shot.png" || upload.body != "PNGDATA" {
		t.Fatalf("upload = %+v", upload)
	}
	if (*seen)[1].path != "/api/v1/sessions/w1/smoke-checks/a/agent-result" {
		t.Fatalf("second request = %s, want the record", (*seen)[1].path)
	}
}

func TestSmokeRecordUsageErrors(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := smokeMultiServer(t, recordedCheck)
	writeRunFileFor(t, cfg, srv)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no session", []string{"smoke", "record", "--case", "a", "--verdict", "pass"}, "session id is required"},
		{"no case", []string{"smoke", "record", "w1", "--verdict", "pass"}, "--case <id> is required"},
		{"bad verdict", []string{"smoke", "record", "w1", "--case", "a", "--verdict", "green"}, "must be pass, fail, or skip"},
		{"nothing to record", []string{"smoke", "record", "w1", "--case", "a"}, "or --evidence"},
		{"unsupported evidence", []string{"smoke", "record", "w1", "--case", "a", "--evidence", "notes.txt"}, "not an accepted evidence type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errOut, err := executeCLI(t, aliveDeps(), tc.args...)
			if err == nil {
				t.Fatalf("expected a usage error; stderr=%s", errOut)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestSmokeRetirePostsTheReason: the reason is the trace, so the command refuses
// to send without one rather than letting a retire become a quiet delete.
func TestSmokeRetirePostsTheReason(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, seen := smokeMultiServer(t, `{"check":{"id":"a","name":"Drag scroll","retiredAt":"2026-08-20T00:00:00Z","retiredReason":"now covered by TestDragScroll"}}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "smoke", "retire", "w1", "--case", "a", "--reason", "now covered by TestDragScroll")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	req := (*seen)[0]
	if req.method != http.MethodPost || req.path != "/api/v1/sessions/w1/smoke-checks/a/retire" {
		t.Fatalf("request = %s %s", req.method, req.path)
	}
	var body retireSmokeCheckRequest
	if err := json.Unmarshal([]byte(req.body), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Reason != "now covered by TestDragScroll" {
		t.Fatalf("reason = %q", body.Reason)
	}
	for _, want := range []string{"retired", "Drag scroll", "now covered by TestDragScroll", "Its results are kept"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
}

func TestSmokeRetireRequiresAReason(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, seen := smokeMultiServer(t, `{}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, aliveDeps(), "smoke", "retire", "w1", "--case", "a")
	if err == nil {
		t.Fatalf("expected a usage error; stderr=%s", errOut)
	}
	if !strings.Contains(err.Error(), "--reason is required") {
		t.Fatalf("error = %v", err)
	}
	if len(*seen) != 0 {
		t.Fatalf("a reasonless retire still called the daemon: %+v", *seen)
	}
}

// TestSmokeListPrintsBothResults: the machine's answer prints under the user's,
// never folded into it, and a retired case prints with why it went.
func TestSmokeListPrintsBothResults(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := smokeMultiServer(t, `{"worker":"fix gl note","checks":[
		{"id":"a","seq":1,"name":"MR appears","verdict":"pending","agentVerdict":"pass","agentNote":"ran clean","agentSha":"abc123def4567890","agentRanAt":"2026-08-20T10:00:00Z"},
		{"id":"b","seq":2,"name":"Drag scroll","verdict":"fail","retiredAt":"2026-08-20T11:00:00Z","retiredReason":"now covered by TestDragScroll"}
	]}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "smoke", "list", "w1")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{
		"CHECK 1 [to check] MR appears",
		"id: a",
		"agent: PASS at abc123def456",
		"agent note: ran clean",
		"RETIRED",
		"reason: now covered by TestDragScroll",
		"kept: user verdict FAIL",
		"retired: 1 case(s), kept with their results",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// The user's line must not carry the machine's verdict.
	if strings.Contains(out, "CHECK 1 [PASS]") {
		t.Errorf("the machine's verdict was rendered as the user's:\n%s", out)
	}
}

// TestSmokeListPrintsTheRunHistory: `ao smoke list` is how an agent reads the
// checklist back, so it has to show that a case's machine result CHANGED - the
// thing a single overwritten column could never say. The current result heads
// the block; every other round is printed under it with its own verdict and
// commit, newest first, and a round that never concluded says so rather than
// borrowing the wording of one that deliberately declined to judge.
func TestSmokeListPrintsTheRunHistory(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := smokeMultiServer(t, `{"worker":"settings copy","checks":[
		{"id":"a","seq":1,"name":"Empty name refused","verdict":"pending",
		 "agentVerdict":"pass","agentNote":"refused with a message","agentSha":"9f10c22a1bbbb","agentRanAt":"2026-08-22T10:00:00Z",
		 "agentEvidence":[{"id":"e1","kind":"image","runId":"r1"},{"id":"e2","kind":"image","runId":""}],
		 "runs":[
		   {"id":"r1","seq":1,"verdict":"fail","note":"saved with an empty name","sha":"d44ad432cffff","recordedAt":"2026-08-21T10:00:00Z"},
		   {"id":"r2","seq":2,"verdict":"pass","note":"refused with a message","sha":"9f10c22a1bbbb","recordedAt":"2026-08-22T10:00:00Z"},
		   {"id":"r3","seq":3,"note":"","sha":""}
		 ]}
	]}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "smoke", "list", "w1")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{
		// The current result is run 2, not the newest ROW (run 3 never concluded).
		"agent: PASS at 9f10c22a1bbb on 2026-08-22T10:00:00Z (run 2 of 3)",
		// An unfinished round is named as one.
		"run 3: never concluded",
		// The earlier verdict survives, with the commit that makes it readable as
		// earlier rather than as wrong, and its own capture.
		"run 1: FAIL at d44ad432cfff on 2026-08-21T10:00:00Z, 1 captured - saved with an empty name",
		// Captures from before AO kept a history are counted apart, not folded in.
		"agent evidence: 2 captured (1 from an unknown run)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// The run whose result heads the block is not repeated underneath it.
	if strings.Contains(out, "run 2:") {
		t.Errorf("the current run was printed twice:\n%s", out)
	}
}

// TestSmokeRecordIgnoresNonSHAGitOutput: the commit is read from a combined
// stdout+stderr capture, so anything that is not a bare object name is dropped -
// a plausible-looking wrong sha would be read as a real staleness signal.
func TestSmokeRecordIgnoresNonSHAGitOutput(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, seen := smokeMultiServer(t, recordedCheck)
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.CommandOutput = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("warning: core.hooksPath overridden\nabc123def4567\n"), nil
	}
	if _, errOut, err := executeCLI(t, deps, "smoke", "record", "w1", "--case", "a", "--verdict", "pass"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var body recordSmokeAgentResultRequest
	if err := json.Unmarshal([]byte((*seen)[0].body), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.SHA != "" {
		t.Fatalf("sha = %q, want empty rather than something that only looks like a commit", body.SHA)
	}
}
