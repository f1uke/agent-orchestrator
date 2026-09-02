package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type sessionRequestLog struct {
	mu       sync.Mutex
	requests []string
}

const cliInvokedRequest = "POST /internal/telemetry/cli-invoked"

func requestLogEntry(r *http.Request) string {
	entry := r.Method + " " + r.URL.Path
	if r.URL.RawQuery != "" {
		entry += "?" + r.URL.RawQuery
	}
	return entry
}

func appendPrimaryRequest(dst *[]string, r *http.Request) {
	entry := requestLogEntry(r)
	if entry == cliInvokedRequest {
		return
	}
	*dst = append(*dst, entry)
}

func (l *sessionRequestLog) append(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	appendPrimaryRequest(&l.requests, r)
}

func (l *sessionRequestLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.requests...)
}

func sessionCommandServer(t *testing.T) (*httptest.Server, *sessionRequestLog) {
	t.Helper()
	log := &sessionRequestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.append(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions":
			active := r.URL.Query().Get("active")
			switch active {
			case "false":
				_, _ = io.WriteString(w, `{"sessions":[`+
					sessionJSON("demo-old", "demo", "worker", "terminated", true)+`,`+
					sessionJSON("demo-orch", "demo", "orchestrator", "terminated", true)+`]}`)
			default:
				_, _ = io.WriteString(w, `{"sessions":[`+
					sessionJSON("demo-2", "demo", "orchestrator", "idle", false)+`,`+
					sessionJSON("demo-1", "demo", "worker", "working", false)+`]}`)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-1", "demo", "worker", "working", false)+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://github.com/aoagents/agent-orchestrator","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/pr/claim":
			var req claimPRRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if !req.AllowTakeover {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"error":"conflict","code":"PR_CLAIMED_BY_ACTIVE_SESSION","message":"PR is already claimed by active session demo-2 (omit --no-takeover to steal)"}`)
				return
			}
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","prs":[{"url":`+jsonQuote(req.PR)+`,"number":142,"state":"open","ci":"passing","review":"review_required","mergeability":"mergeable","reviewComments":false,"updatedAt":"2026-06-04T12:00:00Z"}],"branchChanged":true,"takenOverFrom":["demo-0"]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/cleanup":
			_, _ = io.WriteString(w, `{"ok":true,"cleaned":["demo-old","demo-orch"],"skipped":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/kill":
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","freed":true,"terminated":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/restore":
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","session":`+sessionJSON("demo-1", "demo", "worker", "idle", false)+`}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/sessions/demo-1":
			var req sessionRenameRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","displayName":`+jsonQuote(req.DisplayName)+`}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func sessionJSON(id, project, kind, status string, terminated bool) string {
	b, _ := json.Marshal(map[string]any{
		"id":           id,
		"projectId":    project,
		"kind":         kind,
		"harness":      "codex",
		"displayName":  "Current Name",
		"activity":     map[string]any{"state": "idle", "lastActivityAt": "2026-06-02T12:00:00Z"},
		"isTerminated": terminated,
		"createdAt":    "2026-06-02T11:00:00Z",
		"updatedAt":    "2026-06-02T12:00:00Z",
		"status":       status,
	})
	return string(b)
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestSessionList_ProjectFilterAndDefaultFiltering(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "ls", "--project", "demo")
	if err != nil {
		t.Fatalf("session ls failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "demo:") || !strings.Contains(out, "demo-1") {
		t.Fatalf("output missing worker session:\n%s", out)
	}
	if strings.Contains(out, "demo-2") {
		t.Fatalf("orchestrator session should be hidden without --all:\n%s", out)
	}
	if !strings.Contains(out, "1 terminated session hidden") {
		t.Fatalf("hidden terminated hint missing:\n%s", out)
	}
	want := []string{
		"GET /api/v1/sessions?active=true&project=demo",
		"GET /api/v1/sessions?active=false&project=demo",
	}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionList_JSONOutputDecodes(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "ls", "--project", "demo", "--json")
	if err != nil {
		t.Fatalf("session ls --json failed: %v\nstderr=%s", err, errOut)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("session ls --json output is not decodable: %v\noutput=%s", err, out)
	}
	if got.Meta.HiddenTerminatedCount != 1 {
		t.Fatalf("hiddenTerminatedCount = %d, want 1", got.Meta.HiddenTerminatedCount)
	}
	if len(got.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1; data=%#v", len(got.Data), got.Data)
	}
	if got.Data[0].ID != "demo-1" || got.Data[0].ProjectID != "demo" || got.Data[0].Role != "worker" {
		t.Fatalf("unexpected JSON entry: %#v", got.Data[0])
	}
}

func TestSessionGet_SuccessWithProjectScope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "get", "demo-1", "-p", "demo")
	if err != nil {
		t.Fatalf("session get failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "id: demo-1") || !strings.Contains(out, "project: demo") {
		t.Fatalf("unexpected get output:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions/demo-1"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionGet_JSONOutputDecodes(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "get", "demo-1", "--project", "demo", "--json")
	if err != nil {
		t.Fatalf("session get --json failed: %v\nstderr=%s", err, errOut)
	}
	var got sessionResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("session get --json output is not decodable: %v\noutput=%s", err, out)
	}
	if got.Session.ID != "demo-1" || got.Session.ProjectID != "demo" || got.Session.Status != "working" {
		t.Fatalf("unexpected session JSON: %#v", got.Session)
	}
}

func TestSessionKill_SuccessWithProjectScope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "kill", "demo-1", "--project", "demo")
	if err != nil {
		t.Fatalf("session kill failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "session demo-1 killed") {
		t.Fatalf("unexpected kill output:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions/demo-1", "POST /api/v1/sessions/demo-1/kill"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

// TestSessionKill_PreservedWorkspaceNote: terminated with freed=false means the
// daemon ended the session but kept the worktree (a crewmate is still in it, or
// removal was refused) — the CLI must say so instead of implying a full teardown.
func TestSessionKill_PreservedWorkspaceNote(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/kill" {
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","freed":false,"terminated":true}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "kill", "demo-1")
	if err != nil {
		t.Fatalf("session kill failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "session demo-1 killed (workspace preserved)") {
		t.Fatalf("unexpected kill output:\n%s", out)
	}
}

func TestSessionRestore_SuccessWithProjectScope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restore", "demo-1", "-p", "demo")
	if err != nil {
		t.Fatalf("session restore failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "session demo-1 restored") || !strings.Contains(out, "project: demo") {
		t.Fatalf("unexpected restore output:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions/demo-1", "POST /api/v1/sessions/demo-1/restore"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionCleanup_YesSkipsPrompt(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader("no\n"),
		ProcessAlive: func(int) bool { return true },
	}, "session", "cleanup", "--project", "demo", "--yes")
	if err != nil {
		t.Fatalf("session cleanup failed: %v\nstderr=%s", err, errOut)
	}
	if strings.Contains(out, "Type yes to confirm") {
		t.Fatalf("--yes should skip confirmation prompt:\n%s", out)
	}
	for _, want := range []string{"Checking for completed sessions", "Would clean demo-old", "Would clean demo-orch", "Cleaned: demo-old", "Cleaned: demo-orch", "Cleanup complete. 2 sessions cleaned."} {
		if !strings.Contains(out, want) {
			t.Fatalf("cleanup output missing %q:\n%s", want, out)
		}
	}
	want := []string{
		"GET /api/v1/sessions?active=false&project=demo",
		"POST /api/v1/sessions/cleanup?project=demo",
	}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

// TestSessionCleanup_ReportsSkippedWorkspaces: a session whose workspace was
// preserved must be listed with its reason and counted in the summary —
// previously the CLI printed "Would clean N" then "0 sessions cleaned" with no
// explanation, leaking workspaces invisibly.
func TestSessionCleanup_ReportsSkippedWorkspaces(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"sessions":[`+
				sessionJSON("demo-old", "demo", "worker", "terminated", true)+`,`+
				sessionJSON("demo-orch", "demo", "orchestrator", "terminated", true)+`]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/cleanup":
			_, _ = io.WriteString(w, `{"ok":true,"cleaned":["demo-old"],"skipped":[{"sessionId":"demo-orch","reason":"workspace has uncommitted changes"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "cleanup", "--project", "demo", "--yes")
	if err != nil {
		t.Fatalf("session cleanup failed: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{
		"Cleaned: demo-old",
		"Skipped: demo-orch (workspace has uncommitted changes)",
		"Cleanup complete. 1 session cleaned, 1 skipped.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("cleanup output missing %q:\n%s", want, out)
		}
	}
}

func TestSessionCleanup_PromptFailsWithoutInput(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(""),
		ProcessAlive: func(int) bool { return true },
	}, "session", "cleanup", "--project", "demo")
	if err == nil {
		t.Fatal("expected cleanup prompt without input to fail")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	if !strings.Contains(out, "Type yes to confirm") {
		t.Fatalf("output missing confirmation prompt:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions?active=false&project=demo"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionRename_SuccessWithProjectScope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "rename", "demo-1", "New Name", "-p", "demo")
	if err != nil {
		t.Fatalf("session rename failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, `session demo-1 renamed to "New Name"`) {
		t.Fatalf("unexpected rename output:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions/demo-1", "PATCH /api/v1/sessions/demo-1"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionCommands_MissingIDIsUsageError(t *testing.T) {
	setConfigEnv(t)
	for _, sub := range []string{"get", "kill", "restore"} {
		t.Run(sub, func(t *testing.T) {
			_, _, err := executeCLI(t, Deps{}, "session", sub)
			if err == nil {
				t.Fatal("expected missing id to fail")
			}
			if got := ExitCode(err); got != 2 {
				t.Fatalf("exit code = %d, want 2 (err=%v)", got, err)
			}
		})
	}
}

func TestSessionRename_MissingNameIsUsageError(t *testing.T) {
	setConfigEnv(t)

	_, _, err := executeCLI(t, Deps{}, "session", "rename", "demo-1")
	if err == nil {
		t.Fatal("expected missing name to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2 (err=%v)", got, err)
	}
}

func TestSessionGet_ProjectMismatchDoesNotPassScope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "get", "demo-1", "--project", "other")
	if err == nil {
		t.Fatal("expected project mismatch to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "not in project other") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSessionRename_ProjectMismatchDoesNotPatch(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "rename", "demo-1", "New Name", "--project", "other")
	if err == nil {
		t.Fatal("expected project mismatch to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "not in project other") {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"GET /api/v1/sessions/demo-1"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionClaimPR_ProjectScopeMismatchIsUsage(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "claim-pr", "demo-1", "https://github.com/aoagents/agent-orchestrator/pull/142", "-p", "other")
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "session demo-1 is not in project other") {
		t.Fatalf("err=%v exit=%d, want project mismatch usage", err, ExitCode(err))
	}
	want := []string{"GET /api/v1/sessions/demo-1"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests=%#v want %#v", got, want)
	}
}

func TestSessionClaimPR_JSONAndNoTakeoverError(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "claim-pr", "demo-1", "https://github.com/aoagents/agent-orchestrator/pull/142", "--json")
	if err != nil {
		t.Fatalf("claim-pr --json failed: %v stderr=%s", err, errOut)
	}
	var got claimPRResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.SessionID != "demo-1" || len(got.PRs) != 1 || got.PRs[0].Number != 142 {
		t.Fatalf("bad json err=%v got=%#v out=%s", err, got, out)
	}

	_, _, err = executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "claim-pr", "demo-1", "https://github.com/aoagents/agent-orchestrator/pull/142", "--no-takeover")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "PR_CLAIMED_BY_ACTIVE_SESSION") {
		t.Fatalf("err=%v exit=%d, want takeover refusal runtime error", err, ExitCode(err))
	}
}

func TestSessionClaimPR_GHFallbackWhenProjectRepoMissing(t *testing.T) {
	cfg := setConfigEnv(t)
	log := &sessionRequestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.append(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-1", "demo", "worker", "working", false)+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/pr/claim":
			var req claimPRRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","prs":[{"url":`+jsonQuote(req.PR)+`,"number":142,"state":"open","ci":"passing","review":"review_required","mergeability":"mergeable","reviewComments":false,"updatedAt":"2026-06-04T12:00:00Z"}],"branchChanged":false,"takenOverFrom":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	var ghDir string
	out, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
		CommandOutputInDir: func(_ context.Context, dir, name string, args ...string) ([]byte, error) {
			ghDir = dir
			if name != "gh" {
				t.Fatalf("command name=%s", name)
			}
			return []byte("https://github.com/aoagents/agent-orchestrator\n"), nil
		},
	}, "session", "claim-pr", "demo-1", "142")
	if err != nil {
		t.Fatalf("claim-pr fallback failed: %v", err)
	}
	if ghDir != "/repo/demo" || !strings.Contains(out, "claimed PR #142") {
		t.Fatalf("ghDir=%q out=%s", ghDir, out)
	}
}

func TestSessionClaimGitLabMR(t *testing.T) {
	cfg := setConfigEnv(t)
	const mrURL = "https://gitlab.example.com/group/sub/proj/-/merge_requests/42"
	var gotPR string
	log := &sessionRequestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.append(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-1", "demo", "worker", "working", false)+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"git@gitlab.example.com:group/sub/proj.git","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/pr/claim":
			var req claimPRRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			gotPR = req.PR
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","prs":[{"url":`+jsonQuote(req.PR)+`,"number":42,"state":"open","ci":"passing","review":"review_required","mergeability":"mergeable","reviewComments":false,"updatedAt":"2026-06-04T12:00:00Z"}],"branchChanged":false,"takenOverFrom":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	// A URL with a trailing sub-tab must be normalized to the canonical MR URL.
	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "claim-pr", "demo-1", mrURL+"/commits")
	if err != nil {
		t.Fatalf("claim gitlab MR failed: %v stderr=%s", err, errOut)
	}
	if gotPR != mrURL {
		t.Fatalf("forwarded PR = %q, want normalized %q", gotPR, mrURL)
	}
	if !strings.Contains(out, "claimed PR #42") || !strings.Contains(out, mrURL) {
		t.Fatalf("claim output = %s", out)
	}
}

// newSessionDTO builds the shared daemon session shape for these tests. The CLI
// no longer defines its own copy of it (see sessionDTO), so the nested record
// has to be filled in rather than flattened into one literal.
func newSessionDTO(id, project, status string) sessionDTO {
	return sessionDTO{
		Session: domain.Session{
			SessionRecord: domain.SessionRecord{
				ID:        domain.SessionID(id),
				ProjectID: domain.ProjectID(project),
				Kind:      domain.KindWorker,
			},
			Status: domain.SessionStatus(status),
		},
	}
}

func TestWriteSessionDetailsIncludesReason(t *testing.T) {
	var buf strings.Builder
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	sess := newSessionDTO("demo-1", "demo", "needs_input")
	sess.StatusReason = "active_stale"
	sess.Activity = domain.Activity{State: "active"}
	if err := writeSessionDetails(cmd, sess); err != nil {
		t.Fatalf("writeSessionDetails: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "reason: active_stale") {
		t.Fatalf("output missing reason line:\n%s", out)
	}
}

func TestWriteSessionDetailsOmitsEmptyReason(t *testing.T) {
	var buf strings.Builder
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	sess := newSessionDTO("demo-1", "demo", "working")
	if err := writeSessionDetails(cmd, sess); err != nil {
		t.Fatalf("writeSessionDetails: %v", err)
	}
	if strings.Contains(buf.String(), "reason:") {
		t.Fatalf("empty reason should be omitted:\n%s", buf.String())
	}
}

// The reason rides the existing --json path (which re-marshals sessionDTO), so
// pin the omitempty contract: a reason-less session's JSON is byte-identical to
// before, and a reason is emitted when present.
func TestSessionDTOJSONOmitsEmptyReason(t *testing.T) {
	b, err := json.Marshal(newSessionDTO("demo-1", "demo", "working"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "statusReason") {
		t.Fatalf("empty statusReason should be omitted from JSON:\n%s", b)
	}
}

func TestSessionDTOJSONIncludesReason(t *testing.T) {
	withReason := newSessionDTO("demo-1", "demo", "needs_input")
	withReason.StatusReason = "active_stale"
	b, err := json.Marshal(withReason)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"statusReason":"active_stale"`) {
		t.Fatalf("reason should be present in JSON:\n%s", b)
	}
}

// TestSessionDTOCarriesBranchAndWorkspace closes the gap that made a worktree
// impossible to map back to its session from the CLI.
//
// The CLI used to define its own copy of the session wire shape listing 12
// fields, silently dropping `branch` and `workspacePath` that the daemon
// already returns. Anything needing to know which session owned a worktree had
// to match directory names instead — which is wrong, because a worktree
// directory is named at spawn and never renamed when its branch is.
func TestSessionDTOCarriesBranchAndWorkspace(t *testing.T) {
	sess := newSessionDTO("demo-1", "demo", "terminated")
	sess.Branch = "feature/renamed-after-spawn"
	sess.WorkspacePath = filepath.Join(string(filepath.Separator), "wt", "demo", "feature", "original-name")

	b, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round["branch"] != "feature/renamed-after-spawn" {
		t.Fatalf("branch missing from the CLI session shape: %s", b)
	}
	if round["workspacePath"] != sess.WorkspacePath {
		t.Fatalf("workspacePath missing from the CLI session shape: %s", b)
	}
}

// TestWriteSessionDetailsShowsBranchAndWorkspace: `ao session get` prints them,
// so the mapping is available without --json too.
func TestWriteSessionDetailsShowsBranchAndWorkspace(t *testing.T) {
	var buf strings.Builder
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	sess := newSessionDTO("demo-1", "demo", "terminated")
	sess.Branch = "feature/x"
	sess.WorkspacePath = filepath.Join(string(filepath.Separator), "wt", "demo", "feature", "x")

	if err := writeSessionDetails(cmd, sess); err != nil {
		t.Fatalf("writeSessionDetails: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "branch: feature/x") {
		t.Fatalf("output missing branch line:\n%s", out)
	}
	if !strings.Contains(out, "workspace: "+sess.WorkspacePath) {
		t.Fatalf("output missing workspace line:\n%s", out)
	}
}

// `ao session get` is where an operator (or an agent) asks why a message has not
// arrived. Both facts have to be there: the session is paused, and N messages
// are waiting for it.
func TestWriteSessionDetailsShowsSuspensionAndHeldMessages(t *testing.T) {
	var buf strings.Builder
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	sess := newSessionDTO("demo-1", "demo", "needs_input")
	sess.IsSuspended = true
	sess.QueuedMessages = 2
	sess.QueuedMessagesFailed = 1
	if err := writeSessionDetails(cmd, sess); err != nil {
		t.Fatalf("writeSessionDetails: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "suspended: true") {
		t.Fatalf("output must say the session is paused:\n%s", out)
	}
	if !strings.Contains(out, "queued messages: 2") {
		t.Fatalf("output must say how many messages are waiting:\n%s", out)
	}
	if !strings.Contains(out, "undelivered messages: 1") {
		t.Fatalf("output must surface messages that were given up on:\n%s", out)
	}
}

// A live session with an empty inbox prints exactly what it printed before.
func TestWriteSessionDetailsOmitsQueueLinesWhenThereIsNothingToSay(t *testing.T) {
	var buf strings.Builder
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	if err := writeSessionDetails(cmd, newSessionDTO("demo-1", "demo", "working")); err != nil {
		t.Fatalf("writeSessionDetails: %v", err)
	}
	out := buf.String()
	for _, unwanted := range []string{"suspended:", "queued messages:", "undelivered messages:"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("output should not mention %q for a live session with no held messages:\n%s", unwanted, out)
		}
	}
}

// killRefusalServer answers the plain kill with the daemon's refusal and the
// discard with a success, so a test can drive the whole two-step exactly as the
// daemon presents it.
func killRefusalServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sessions/demo-1/kill" {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		if strings.Contains(string(raw), `"discardUncommitted":true`) {
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","freed":true,"terminated":true,`+
				`"discarded":[{"path":"src/main.go","status":"modified"},{"path":"NewFile.swift","status":"untracked"}],`+
				`"preservedRef":"refs/ao/preserved/demo-1"}`)
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"conflict","code":"SESSION_HAS_UNDELIVERED_WORK",`+
			`"message":"demo-1 still holds 2 uncommitted files that no pull request carries, so it was not killed and nothing was torn down. Finish and deliver the work, or discard it deliberately.",`+
			`"details":{"reason":"workspace_dirty","files":[{"path":"src/main.go","status":"modified"},{"path":"NewFile.swift","status":"untracked"}]}}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

// The incident's command. A refused kill must FAIL - non-zero exit - and say
// which files refused it. The old output was "session demo-1 killed (workspace
// preserved)" and exit 0 over a row nothing had touched.
func TestSessionKill_RefusalFailsAndNamesTheFiles(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, bodies := killRefusalServer(t)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "kill", "demo-1")
	if err == nil {
		t.Fatalf("a refused kill exited 0; output was:\n%s", out)
	}
	msg := err.Error()
	for _, want := range []string{"src/main.go", "NewFile.swift", "modified", "untracked", "--discard-uncommitted"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal must contain %q; got:\n%s", want, msg)
		}
	}
	if strings.Contains(out, "killed") {
		t.Fatalf("a refused kill printed a success line:\n%s", out)
	}
	if len(*bodies) != 1 {
		t.Fatalf("requests = %v, want exactly one (the refused kill)", *bodies)
	}
}

// The deliberate path: the files are printed BEFORE they go, and the plain kill
// that produced that list is the same side-effect-free probe the daemon refuses
// with - so a discard can never happen without the list being shown, even when
// the flag was typed straight away.
func TestSessionKill_DiscardListsTheWorkBeforeDestroyingIt(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, bodies := killRefusalServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "kill", "demo-1", "--discard-uncommitted")
	if err != nil {
		t.Fatalf("deliberate discard failed: %v\nstderr=%s", err, errOut)
	}
	if len(*bodies) != 2 || !strings.Contains((*bodies)[1], `"discardUncommitted":true`) {
		t.Fatalf("requests = %v, want the preview then the discard", *bodies)
	}
	listedAt := strings.Index(out, "src/main.go")
	discardedAt := strings.Index(out, "discarded 2 file(s)")
	if listedAt < 0 || discardedAt < 0 || listedAt > discardedAt {
		t.Fatalf("the files must be listed BEFORE the discard is reported; got:\n%s", out)
	}
	if !strings.Contains(out, "refs/ao/preserved/demo-1") {
		t.Fatalf("output must say where the work was captured:\n%s", out)
	}
	if !strings.Contains(out, "session demo-1 killed") {
		t.Fatalf("the discard did not report the kill:\n%s", out)
	}
}

// terminated=false is the shape that started all this: the daemon did nothing,
// so the CLI may not print a success line for it.
func TestSessionKill_UnterminatedResultIsNotReportedAsAKill(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/kill" {
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","freed":false,"terminated":false}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "kill", "demo-1")
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	if !strings.Contains(out, "was NOT killed") {
		t.Fatalf("output = %q, want it to say the session was not killed", out)
	}
}
