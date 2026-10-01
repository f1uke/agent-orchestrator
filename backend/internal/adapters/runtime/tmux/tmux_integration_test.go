package tmux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRuntimeIntegration(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}

	ctx := context.Background()
	id := strings.ReplaceAll(t.Name(), "/", "_")
	r := New(Options{Timeout: 5 * time.Second, SocketDir: privateSocketDir(t)})

	// Ensure clean slate: ignore errors (session may not exist).
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: id})

	t.Cleanup(func() {
		// Always destroy so a test failure never leaks a tmux session.
		_ = r.Destroy(context.Background(), ports.RuntimeHandle{ID: id})
	})

	h, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(id),
		WorkspacePath: t.TempDir(),
		// Run a trivial command then drop into an interactive shell (the keep-alive
		// exec is added by buildLaunchCommand, but we also verify here that output
		// appears).
		Argv: []string{"sh", "-c", "echo hello-from-tmux"},
		Env:  map[string]string{"AO_SESSION_ID": id},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	alive, err := r.IsAlive(ctx, h)
	if err != nil {
		t.Fatalf("IsAlive: %v", err)
	}
	if !alive {
		t.Fatal("alive = false, want true after create")
	}

	// Wait for the echo output to appear (the session may take a moment to
	// write it to the pane history).
	out := waitForOutput(t, r, h, "hello-from-tmux", 5*time.Second)
	if !strings.Contains(out, "hello-from-tmux") {
		t.Fatalf("output = %q, want hello-from-tmux", out)
	}

	// Send a command and verify it echoes back.
	if err := r.SendMessage(ctx, h, "echo hello-send"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	out = waitForOutput(t, r, h, "hello-send", 5*time.Second)
	if !strings.Contains(out, "hello-send") {
		t.Fatalf("output after SendMessage = %q, want hello-send", out)
	}

	// Destroy and verify liveness goes false.
	if err := r.Destroy(ctx, h); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	alive, err = r.IsAlive(ctx, h)
	if err != nil {
		t.Fatalf("IsAlive after destroy: %v", err)
	}
	if alive {
		t.Fatal("alive after destroy = true, want false")
	}
}

// TestRuntimeIntegrationExactSessionParsing verifies that IsAlive uses exact
// session matching and does not treat a prefix as a live session.
func TestRuntimeIntegrationExactSessionParsing(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}

	ctx := context.Background()
	base := strings.ReplaceAll(t.Name(), "/", "_")
	longID := base + "_long"
	prefixID := base

	r := New(Options{Timeout: 5 * time.Second, SocketDir: privateSocketDir(t)})
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: longID})
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: prefixID})

	t.Cleanup(func() {
		_ = r.Destroy(context.Background(), ports.RuntimeHandle{ID: longID})
		_ = r.Destroy(context.Background(), ports.RuntimeHandle{ID: prefixID})
	})

	h, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(longID),
		WorkspacePath: t.TempDir(),
		Argv:          []string{"sh", "-c", "echo ready"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// tmux has-session -t <prefix> should NOT match <longID> because tmux
	// requires the exact session name when using -t with a plain string (not a
	// glob). Verify by probing the prefix handle directly.
	prefixAlive, err := r.IsAlive(ctx, ports.RuntimeHandle{ID: prefixID})
	if err != nil {
		// tmux may return an error (session not found) rather than exit 0.
		// That is acceptable here: the point is the prefix must not be alive.
		t.Logf("IsAlive prefix returned error (acceptable): %v", err)
	}
	if prefixAlive {
		_ = r.Destroy(ctx, h)
		t.Fatal("prefix handle reported alive; tmux session matching is not exact")
	}
}

// waitForOutput polls GetOutput until out contains want or the deadline passes.
func waitForOutput(t *testing.T, r *Runtime, h ports.RuntimeHandle, want string, deadline time.Duration) string {
	t.Helper()
	end := time.Now().Add(deadline)
	var out string
	for time.Now().Before(end) {
		var err error
		out, err = r.GetOutput(context.Background(), h, 50)
		if err != nil {
			t.Fatalf("GetOutput: %v", err)
		}
		if strings.Contains(out, want) {
			return out
		}
		time.Sleep(100 * time.Millisecond)
	}
	return out
}

// TestKillServerInsideAPaneEndsOnlyThatSession replays the 2026-10-01 incident
// against real tmux: an agent tearing down a sandbox ran
// `TMUX_TMPDIR=<sandbox> tmux kill-server` from its own pane. `$TMUX` in the
// pane wins over TMUX_TMPDIR, so the command addressed the server hosting the
// pane - which, while AO shared one server, held every session there was. With
// a server per session it can end only the session that ran it.
func TestKillServerInsideAPaneEndsOnlyThatSession(t *testing.T) {
	sockDir := privateSocketDir(t)
	ctx := context.Background()
	r := New(Options{Timeout: 5 * time.Second, Shell: "/bin/sh", SocketDir: sockDir})

	bystander, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID: "bystander", WorkspacePath: t.TempDir(), Argv: []string{"sleep", "600"},
	})
	if err != nil {
		t.Fatalf("Create bystander: %v", err)
	}
	// The culprit waits for a go signal, so Create sees it alive first.
	trigger := filepath.Join(t.TempDir(), "go")
	culprit, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID: "culprit", WorkspacePath: t.TempDir(),
		Argv: []string{"sh", "-c", `while [ ! -e "$1" ]; do sleep 0.05; done; ` +
			`test -n "$TMUX" || { echo "TMUX unset in pane" >&2; exit 1; }; ` +
			`TMUX_TMPDIR=` + t.TempDir() + ` tmux kill-server`, "sh", trigger},
	})
	if err != nil {
		t.Fatalf("Create culprit: %v", err)
	}
	if err := os.WriteFile(trigger, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		// A probe that lands while the server is going down errors ("server
		// exited unexpectedly"); only a definitive "gone" ends the wait.
		if alive, err := r.IsAlive(ctx, culprit); err == nil && !alive {
			break
		}
		if time.Now().After(deadline) {
			out, _ := r.GetOutput(ctx, culprit, 20)
			t.Fatalf("the culprit's kill-server never ended its own session. Pane:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	alive, err := r.IsAlive(ctx, bystander)
	if err != nil {
		t.Fatalf("IsAlive bystander: %v", err)
	}
	if !alive {
		t.Fatal("a kill-server inside one session's pane took down another session")
	}
	agent, err := r.AgentAlive(ctx, bystander)
	if err != nil || !agent {
		t.Fatalf("bystander's agent alive = %v, %v; want its agent untouched", agent, err)
	}
}
