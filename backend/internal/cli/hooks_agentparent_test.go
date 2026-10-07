package cli

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// The pane's leader shell is pid 100 and the claude running the hook is pid 200.
// Only a claude whose parent is the leader is the session's agent: a `claude mcp
// list` run from the agent's Bash tool, a background session Claude Code's own
// daemon hosts, and an agent orphaned by a restart all share AO_SESSION_ID and
// the worktree's hooks, and none of them may end the session.
func TestHooks_OnlyTheSessionsOwnAgentReports(t *testing.T) {
	cases := []struct {
		name       string
		parent     func(int) (int, error)
		wantPosted bool
	}{
		{name: "the agent itself", parent: parentIs(100), wantPosted: true},
		{name: "a claude nested in the agent", parent: parentIs(300), wantPosted: false},
		{name: "an agent orphaned by a restart", parent: parentIs(1), wantPosted: false},
		{name: "a parent that cannot be read", parent: func(int) (int, error) { return 0, errors.New("no such process") }, wantPosted: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "ao-7")
			t.Setenv("AO_AGENT_PARENT_PID", "100")
			t.Setenv("CLAUDE_PID", "200")
			cfg := setConfigEnv(t)
			srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
			writeRunFileFor(t, cfg, srv)

			_, _, err := executeCLI(t, Deps{
				In:            strings.NewReader(`{"reason":"other"}`),
				ProcessAlive:  func(int) bool { return true },
				ProcessParent: c.parent,
			}, "hooks", "claude-code", "session-end")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if posted := capture.hits > 0; posted != c.wantPosted {
				t.Fatalf("posted = %v, want %v (body %q)", posted, c.wantPosted, capture.body)
			}
			if c.wantPosted && capturedState(t, capture) != "exited" {
				t.Errorf("state = %q, want exited", capturedState(t, capture))
			}
		})
	}
}

// A session launched before AO exported the leader's pid has nothing to compare
// against, so its hooks report exactly as they always did.
func TestHooks_ReportsWhenTheLeaderIsUnknown(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "ao-7")
	t.Setenv("AO_AGENT_PARENT_PID", "")
	t.Setenv("CLAUDE_PID", "200")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:            strings.NewReader(`{}`),
		ProcessAlive:  func(int) bool { return true },
		ProcessParent: parentIs(300),
	}, "hooks", "claude-code", "stop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := capturedState(t, capture); got != "idle" {
		t.Errorf("state = %q, want idle", got)
	}
}

func parentIs(ppid int) func(int) (int, error) {
	return func(pid int) (int, error) {
		if pid != 200 {
			return 0, errors.New("unexpected pid")
		}
		return ppid, nil
	}
}
