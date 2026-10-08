package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const incidentSession = "nter-ios-app-88"

func setIncidentJobEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AO_SESSION_ID", incidentSession)
	t.Setenv("AO_SESSION_KIND", "worker")
	t.Setenv("AO_WORKSPACE", "")
	t.Setenv("CLAUDE_CODE_SESSION_KIND", "bg")
	t.Setenv("AO_AGENT_PARENT_PID", "")
}

func worktrees(t *testing.T, cfg testConfig) (own, other string) {
	t.Helper()
	data := cfg.dataDir
	own = filepath.Join(data, "worktrees", "nter-ios-app", "feature", "openfeature-ios-research")
	other = filepath.Join(data, "worktrees", "agent-orchestrator", "feature", "ios-run-bar-progress")
	for _, dir := range []string{own, other} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return own, other
}

func sendAsSession(t *testing.T, cfg testConfig) (*sendCapture, string, error) {
	t.Helper()
	srv, capture := sendServer(t, http.StatusOK, `{"ok":true,"sessionId":"demo-1","message":"hi"}`)
	writeRunFileFor(t, cfg, srv)
	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"send", "--session", "demo-1", "--message", "hi")
	return capture, errOut, err
}

func TestSend_RefusesInsideAClaudeCodeBackgroundSession(t *testing.T) {
	setIncidentJobEnv(t)

	capture, _, err := sendAsSession(t, setConfigEnv(t))
	if err == nil {
		t.Fatalf("send went through as %s: body %q", incidentSession, capture.body)
	}
	if capture.path != "" {
		t.Errorf("daemon was called (%s) by a process that is not %s", capture.path, incidentSession)
	}
	if !strings.Contains(err.Error(), incidentSession) {
		t.Errorf("error does not name the claimed session: %v", err)
	}
}

func TestSend_RefusesAWorkerEnvInAnotherSessionsWorktree(t *testing.T) {
	cfg := setConfigEnv(t)
	own, other := worktrees(t, cfg)
	t.Setenv("AO_SESSION_ID", incidentSession)
	t.Setenv("AO_SESSION_KIND", "worker")
	t.Setenv("AO_WORKSPACE", own)
	t.Setenv("CLAUDE_CODE_SESSION_KIND", "")
	t.Chdir(other)

	capture, _, err := sendAsSession(t, cfg)
	if err == nil {
		t.Fatalf("send went through as %s from %s", incidentSession, other)
	}
	if capture.path != "" {
		t.Errorf("daemon was called (%s)", capture.path)
	}
	if !strings.Contains(err.Error(), other) {
		t.Errorf("error does not name the worktree it ran in: %v", err)
	}
}

func TestSend_TrustsTheEnvWhereTheSessionOwnsTheWorkingDirectory(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		cwd      func(own, other string) string
		wantWarn bool
	}{
		{name: "a worker in its own worktree", kind: "worker", cwd: func(own, _ string) string { return own }},
		{name: "a worker in a subdirectory of its worktree", kind: "worker", cwd: func(own, _ string) string {
			sub := filepath.Join(own, "backend")
			_ = os.MkdirAll(sub, 0o750)
			return sub
		}},
		{name: "a worker in a scratch directory", kind: "worker", cwd: func(string, string) string { return t.TempDir() }},
		{name: "an orchestrator reading a worker's worktree", kind: "orchestrator", cwd: func(_, other string) string { return other }, wantWarn: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			own, other := worktrees(t, cfg)
			t.Setenv("AO_SESSION_ID", incidentSession)
			t.Setenv("AO_SESSION_KIND", c.kind)
			t.Setenv("AO_WORKSPACE", own)
			t.Setenv("CLAUDE_CODE_SESSION_KIND", "")
			t.Chdir(c.cwd(own, other))

			capture, errOut, err := sendAsSession(t, cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
			}
			if capture.path != "/api/v1/sessions/demo-1/send" {
				t.Errorf("path = %q, want the send route", capture.path)
			}
			if warned := strings.Contains(errOut, incidentSession); warned != c.wantWarn {
				t.Errorf("warned = %v, want %v (stderr %q)", warned, c.wantWarn, errOut)
			}
		})
	}
}

func TestHooks_DropsCallbacksFromAClaudeCodeBackgroundSession(t *testing.T) {
	setIncidentJobEnv(t)
	t.Setenv("CLAUDE_PID", "46321")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"hook_event_name":"Stop"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "stop")
	if err != nil {
		t.Fatalf("a hook must never fail the agent: %v", err)
	}
	if capture.hits != 0 {
		t.Errorf("reported %s as %s (%d posts)", capture.body, incidentSession, capture.hits)
	}
}
