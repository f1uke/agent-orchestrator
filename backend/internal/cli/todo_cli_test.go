package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// `ao spawn --todo` posts startImmediately=false + createdBy, skips the agent
// auth preflight (nothing launches yet), and prints a queued-TODO message.
func TestSpawnTodo_CreatesDeferred(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "demo-orch")
	var requests []string
	var gotBody spawnRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appendPrimaryRequest(&requests, r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://github.com/aoagents/agent-orchestrator","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_, _ = io.WriteString(w, `{"session":{"id":"demo-9","status":"todo","projectId":"demo"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"spawn", "--from", "main", "--project", "demo", "--agent", "codex", "--name", "worker", "--prompt", "do it", "--todo")
	if err != nil {
		t.Fatalf("spawn --todo failed: %v stderr=%s", err, errOut)
	}
	if gotBody.StartImmediately == nil || *gotBody.StartImmediately {
		t.Fatalf("startImmediately = %v, want false pointer", gotBody.StartImmediately)
	}
	if gotBody.CreatedBy != "demo-orch" {
		t.Fatalf("createdBy = %q, want demo-orch (from AO_SESSION_ID)", gotBody.CreatedBy)
	}
	if !strings.Contains(out, "queued TODO session demo-9") {
		t.Fatalf("output missing queued-TODO line: %s", out)
	}
	if strings.Contains(out, "attach with:") {
		t.Fatalf("a queued TODO must not print an attach hint: %s", out)
	}
	// The agent auth preflight (POST /agents/refresh) is skipped for a TODO.
	want := []string{"GET /api/v1/projects/demo", "POST /api/v1/sessions"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%#v want %#v", requests, want)
	}
}

// `ao spawn --todo --claim-pr` is rejected as usage (a queued task has no live
// session to claim into) and exits 2.
func TestSpawnTodo_RejectsClaimPR(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) }))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"spawn", "--from", "main", "--project", "demo", "--todo", "--claim-pr", "142")
	if err == nil {
		t.Fatal("expected usage error for --todo --claim-pr")
	}
	if ExitCode(err) != 2 {
		t.Fatalf("exit code = %d, want 2 (usage)", ExitCode(err))
	}
}

// `ao session start <id>` posts to the start route and prints a started message.
func TestSessionStart(t *testing.T) {
	cfg := setConfigEnv(t)
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appendPrimaryRequest(&requests, r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-9/start":
			_, _ = io.WriteString(w, `{"session":{"id":"demo-9","status":"idle","projectId":"demo","branch":"feature/x"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "start", "demo-9")
	if err != nil {
		t.Fatalf("session start failed: %v stderr=%s", err, errOut)
	}
	if !strings.Contains(out, "started session demo-9") {
		t.Fatalf("output missing started line: %s", out)
	}
	want := []string{"POST /api/v1/sessions/demo-9/start"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%#v want %#v", requests, want)
	}
}
