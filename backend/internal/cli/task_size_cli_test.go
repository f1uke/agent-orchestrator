package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// `ao spawn --task-size mechanical` forwards taskSize in the POST body (lower-
// cased/normalized) so the daemon can right-size the worker's ceremony.
func TestSpawn_ForwardsTaskSize(t *testing.T) {
	cfg := setConfigEnv(t)
	var gotBody spawnRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://github.com/aoagents/agent-orchestrator","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_, _ = io.WriteString(w, `{"session":{"id":"demo-9","status":"idle","projectId":"demo","branch":"feature/x"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"spawn", "--from", "main", "--project", "demo", "--agent", "codex", "--name", "worker",
		"--prompt", "rename it", "--skip-agent-check", "--task-size", "MECHANICAL")
	if err != nil {
		t.Fatalf("spawn --task-size failed: %v stderr=%s", err, errOut)
	}
	if gotBody.TaskSize != "mechanical" {
		t.Fatalf("taskSize = %q, want mechanical (normalized lowercase)", gotBody.TaskSize)
	}
}

// An omitted --task-size sends no taskSize field, letting the daemon default it
// to standard.
func TestSpawn_OmitsTaskSizeByDefault(t *testing.T) {
	cfg := setConfigEnv(t)
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://github.com/aoagents/agent-orchestrator","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_ = json.NewDecoder(r.Body).Decode(&raw)
			_, _ = io.WriteString(w, `{"session":{"id":"demo-9","status":"idle","projectId":"demo","branch":"feature/x"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"spawn", "--from", "main", "--project", "demo", "--agent", "codex", "--name", "worker",
		"--prompt", "add a feature", "--skip-agent-check"); err != nil {
		t.Fatalf("spawn failed: %v stderr=%s", err, errOut)
	}
	if _, ok := raw["taskSize"]; ok {
		t.Fatalf("taskSize should be omitted when not passed, got %v", raw["taskSize"])
	}
}

// `ao spawn --task-size bogus` is rejected as usage (exit 2) before any daemon
// round-trip.
func TestSpawn_RejectsInvalidTaskSize(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) }))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"spawn", "--from", "main", "--project", "demo", "--task-size", "bogus")
	if err == nil {
		t.Fatal("expected usage error for --task-size bogus")
	}
	if ExitCode(err) != 2 {
		t.Fatalf("exit code = %d, want 2 (usage)", ExitCode(err))
	}
}
