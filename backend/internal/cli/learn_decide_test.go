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
)

// learnServer answers each path with its canned response and status, and keeps
// every decision body it got.
func learnServer(t *testing.T, routes map[string]struct {
	code int
	body string
}) (*httptest.Server, *[]smokeRequest) {
	t.Helper()
	var seen []smokeRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, smokeRequest{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: string(body)})
		route, ok := routes[r.URL.Path]
		if !ok {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if route.code != 0 {
			w.WriteHeader(route.code)
		}
		_, _ = io.WriteString(w, route.body)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

type route = struct {
	code int
	body string
}

const learnList = `{"proposals":[
{"id":12,"projectId":"p","action":"create_memory","targetPath":"/m/feedback_a.md","scope":"project:p","title":"Snoozed one","status":"pending","snoozedUntil":"2099-01-02T00:00:00Z","confidence":0.8,"outcome":"merged"},
{"id":11,"projectId":"p","action":"create_memory","targetPath":"/m/feedback_b.md","scope":"project:p","title":"Waiting one","status":"pending","confidence":0.7,"outcome":"merged"}
],"run":{}}`

func TestLearnProposals_ShowsTheSnoozeAndFiltersByIt(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, seen := learnServer(t, map[string]route{"/api/v1/learning/proposals": {body: learnList}})
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, aliveDeps(), "learn", "proposals")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "#12 snoozed until 2099-01-02") || !strings.Contains(out, "#11 pending") {
		t.Errorf("a snoozed proposal must say so, with its date:\n%s", out)
	}
	out, _, err = executeCLI(t, aliveDeps(), "learn", "proposals", "--status", "snoozed")
	if err != nil || !strings.Contains(out, "#12") || strings.Contains(out, "#11") {
		t.Errorf("--status snoozed:\n%s %v", out, err)
	}
	out, _, err = executeCLI(t, aliveDeps(), "learn", "proposals", "--status", "waiting")
	if err != nil || strings.Contains(out, "#12") || !strings.Contains(out, "#11") {
		t.Errorf("--status waiting:\n%s %v", out, err)
	}
	if _, _, err := executeCLI(t, aliveDeps(), "learn", "proposals", "--status", "rejected"); err != nil {
		t.Fatal(err)
	}
	if last := (*seen)[len(*seen)-1]; last.query != "all=true" {
		t.Errorf("a settled status needs every proposal: query %q", last.query)
	}
	if _, _, err := executeCLI(t, aliveDeps(), "learn", "proposals", "--status", "maybe"); !isUsageError(err) {
		t.Errorf("an unknown status is a usage error: %v", err)
	}
}

func TestLearnDecisions_PostWhoDecided(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "agent-orchestrator-355")
	ok := `{"id":3,"status":"pending","targetPath":"/m/feedback_a.md","rejectReason":"not a rule"}`
	srv, seen := learnServer(t, map[string]route{
		"/api/v1/learning/proposals/3/reject":   {body: ok},
		"/api/v1/learning/proposals/3/reopen":   {body: ok},
		"/api/v1/learning/proposals/3/unsnooze": {body: ok},
		"/api/v1/learning/proposals/3/approve":  {body: ok},
		"/api/v1/learning/proposals/3/undo":     {body: ok},
	})
	writeRunFileFor(t, cfg, srv)

	if _, _, err := executeCLI(t, aliveDeps(), "learn", "reject", "3"); !isUsageError(err) {
		t.Errorf("reject without a reason is a usage error: %v", err)
	}
	for _, args := range [][]string{
		{"learn", "reject", "#3", "--reason", "not a rule"},
		{"learn", "reopen", "3"},
		{"learn", "unsnooze", "3"},
		{"learn", "approve", "3"},
		{"learn", "undo", "3", "--confirm", "tok"},
		{"learn", "proposals", "reject", "3", "--reason", "old spelling"},
	} {
		if out, errOut, err := executeCLI(t, aliveDeps(), args...); err != nil {
			t.Fatalf("%v: %v %s %s", args, err, out, errOut)
		}
	}
	if len(*seen) != 6 {
		t.Fatalf("requests = %+v", *seen)
	}
	for _, req := range *seen {
		var body map[string]any
		if err := json.Unmarshal([]byte(req.body), &body); err != nil {
			t.Fatal(err)
		}
		if body["via"] != "cli" || body["session"] != "agent-orchestrator-355" {
			t.Errorf("%s body = %v, want via cli and the session", req.path, body)
		}
	}
	var undo map[string]any
	_ = json.Unmarshal([]byte((*seen)[4].body), &undo)
	if undo["confirmToken"] != "tok" {
		t.Errorf("undo body = %v", undo)
	}
}

func TestLearnUndo_AChangedFilePrintsTheDiffAndTheToken(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := learnServer(t, map[string]route{
		"/api/v1/learning/proposals/5/undo": {code: http.StatusConflict, body: `{"error":"conflict","code":"PROPOSAL_CHANGED","message":"changed",
			"details":{"path":"/m/feedback_a.md","diff":"--- a/m/feedback_a.md\n+++ b/m/feedback_a.md\n@@ -1 +1 @@\n-mine\n+theirs\n","token":"9f8e"}}`},
		"/api/v1/learning/proposals/5/edit": {body: `{"id":5,"status":"applied","targetPath":"/m/feedback_a.md"}`},
	})
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, aliveDeps(), "learn", "undo", "5")
	if err == nil || isUsageError(err) {
		t.Fatalf("a changed file is a runtime refusal: %v", err)
	}
	if !strings.Contains(errOut, "+theirs") || !strings.Contains(errOut, "--confirm 9f8e") {
		t.Errorf("stderr = %q", errOut)
	}

	if _, _, err := executeCLI(t, aliveDeps(), "learn", "edit", "5"); !isUsageError(err) {
		t.Errorf("edit without --file: %v", err)
	}
	f := filepath.Join(t.TempDir(), "m.md")
	if err := os.WriteFile(f, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeCLI(t, aliveDeps(), "learn", "edit", "5", "--file", f)
	if err != nil || !strings.Contains(out, "#5 edited /m/feedback_a.md") {
		t.Errorf("edit = %q %v", out, err)
	}
}

func TestLearnProposalShow_PrintsHistoryAndWhatIsWrittenNow(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := learnServer(t, map[string]route{"/api/v1/learning/proposals/5": {body: `{"proposal":{"id":5,"status":"applied","action":"create_memory",
		"targetPath":"/m/feedback_a.md","decidedAt":"2026-10-04T10:00:00Z","title":"t"},"evidence":[],
		"written":{"path":"/m/feedback_a.md","exists":true,"content":"theirs\n","changed":true,"diff":"-mine\n+theirs\n","token":"9f8e"},
		"history":[{"kind":"snoozed","status":"pending","snoozedUntil":"2027-01-02T00:00:00Z","via":"app","at":"2026-10-04T09:00:00Z"},
		{"kind":"approved","status":"applied","via":"cli","session":"agent-orchestrator-355","at":"2026-10-04T10:00:00Z"}]}`}})
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, aliveDeps(), "learn", "proposals", "show", "5")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"snoozed until 2027-01-02  (app)", "approved  (cli agent-orchestrator-355)", "changed since AO wrote it (undo or edit needs --confirm 9f8e)", "+theirs"} {
		if !strings.Contains(out, want) {
			t.Errorf("show is missing %q:\n%s", want, out)
		}
	}
}

func isUsageError(err error) bool { return errors.As(err, &usageError{}) }
