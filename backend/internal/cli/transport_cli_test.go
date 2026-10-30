package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func transportSpawnServer(t *testing.T, body *map[string]any) {
	t.Helper()
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://github.com/aoagents/agent-orchestrator","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_ = json.NewDecoder(r.Body).Decode(body)
			_, _ = io.WriteString(w, `{"session":{"id":"demo-9","status":"idle","projectId":"demo","branch":"feature/x"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
}

func TestSpawn_Transport(t *testing.T) {
	base := []string{"spawn", "--from", "main", "--project", "demo", "--name", "worker", "--prompt", "go", "--skip-agent-check"}
	run := func(t *testing.T, extra ...string) (map[string]any, error) {
		t.Helper()
		body := map[string]any{}
		transportSpawnServer(t, &body)
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, append(base, extra...)...)
		return body, err
	}

	t.Run("chat is forwarded", func(t *testing.T) {
		body, err := run(t, "--agent", "claude-code", "--transport", "chat")
		if err != nil {
			t.Fatal(err)
		}
		if body["transport"] != "chat" {
			t.Fatalf("transport = %v, want chat", body["transport"])
		}
	})
	t.Run("omitted sends nothing", func(t *testing.T) {
		body, err := run(t, "--agent", "codex")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := body["transport"]; ok {
			t.Fatalf("transport = %v, want it omitted so the daemon defaults it", body["transport"])
		}
	})
	t.Run("unknown value is a usage error", func(t *testing.T) {
		body, err := run(t, "--transport", "pigeon")
		if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), `--transport must be terminal or chat, not "pigeon"`) {
			t.Fatalf("err = %v exit = %d", err, ExitCode(err))
		}
		if len(body) != 0 {
			t.Fatal("a usage error reached the daemon")
		}
	})
	t.Run("chat with another agent is a usage error", func(t *testing.T) {
		body, err := run(t, "--agent", "codex", "--transport", "chat")
		if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "--transport chat needs --harness claude-code, not codex") {
			t.Fatalf("err = %v exit = %d", err, ExitCode(err))
		}
		if len(body) != 0 {
			t.Fatal("a usage error reached the daemon")
		}
	})
}
