package cli

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/claudeprofile"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

func runDriftCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := NewRootCommand(Deps{Out: &out, Err: &out, HTTPClient: &http.Client{}, ProcessAlive: func(int) bool { return true }})
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestE2E_SpawnSendsTheClaudeProfile(t *testing.T) {
	for _, todo := range []bool{false, true} {
		sessions := &fakeSessionService{}
		startDriftTestDaemon(t, sessions, &fakeProjectManager{})
		args := []string{"spawn", "--project", "mer", "--harness", "claude-code", "--from", "main", "--prompt", "hi", "--name", "w", "--skip-agent-check", "--claude-profile", "OmniRoute"}
		if todo {
			args = append(args, "--todo")
		}
		if out, err := runDriftCLI(t, args...); err != nil {
			t.Fatalf("spawn (todo=%v): %v\n%s", todo, err, out)
		}
		if sessions.spawned.ClaudeProfile != "OmniRoute" {
			t.Fatalf("todo=%v: SpawnConfig.ClaudeProfile = %q, want OmniRoute", todo, sessions.spawned.ClaudeProfile)
		}
	}
}

func TestE2E_SessionSetClaudeProfileSaysWhatHappened(t *testing.T) {
	cases := map[domain.ClaudeProfileRestart]string{
		domain.ClaudeProfileRestarted:      "restarted demo-1 on Claude profile OmniRoute",
		domain.ClaudeProfileRestartPending: "demo-1 restarts on Claude profile OmniRoute once its agent is idle",
		domain.ClaudeProfileNextLaunch:     "demo-1 uses Claude profile OmniRoute from its next restart",
	}
	for outcome, want := range cases {
		sessions := &fakeSessionService{claudeProfileOutcome: outcome}
		startDriftTestDaemon(t, sessions, &fakeProjectManager{})

		out, err := runDriftCLI(t, "session", "set-claude-profile", "demo-1", "omniroute", "--restart")
		if err != nil {
			t.Fatalf("%s: %v\n%s", outcome, err, out)
		}
		if sessions.claudeProfileCall != (claudeProfileCall{id: "demo-1", profile: "omniroute", restart: true}) {
			t.Fatalf("%s: daemon got %+v", outcome, sessions.claudeProfileCall)
		}
		if !strings.Contains(out, want) {
			t.Fatalf("%s: output %q, want %q", outcome, out, want)
		}
	}
}

func TestSessionSetClaudeProfile_MissingProfileIsUsageError(t *testing.T) {
	setConfigEnv(t)
	for _, args := range [][]string{{"demo-1"}, {"demo-1", " "}} {
		_, _, err := executeCLI(t, Deps{}, append([]string{"session", "set-claude-profile"}, args...)...)
		if got := ExitCode(err); got != 2 {
			t.Fatalf("args %q: exit code = %d, want 2 (err=%v)", args, got, err)
		}
	}
}

func TestE2E_ClaudeProfileListShowsBuiltinsAndUserProfiles(t *testing.T) {
	store, err := claudeprofile.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetUser([]claudeprofile.Profile{{Name: "Work", SettingsFile: "~/.claude/settings-work.json"}}); err != nil {
		t.Fatal(err)
	}
	startDriftTestDaemonWith(t, httpd.APIDeps{ClaudeProfiles: store})

	out, err := runDriftCLI(t, "claude-profile", "ls")
	if err != nil {
		t.Fatalf("claude-profile ls: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "NAME") {
		t.Fatalf("output:\n%s\nwant a header and three profiles", out)
	}
	for i, want := range [][]string{
		{"Subscription", "-", "yes"},
		{"OmniRoute", "~/.claude/settings-omniroute.json", "yes"},
		{"Work", "~/.claude/settings-work.json", "no"},
	} {
		if got := strings.Fields(lines[i+1]); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("row %d = %q, want %q", i+1, got, want)
		}
	}
}
