package tmux

import (
	"reflect"
	"testing"
)

func TestStripEnvKeys(t *testing.T) {
	t.Run("drops daemon-only AO keys so they do not propagate into worker panes", func(t *testing.T) {
		in := []string{
			"PATH=/usr/bin",
			"AO_SESSION_IDLE_CLOSE=3h",
			"AO_OWNER=app",
			"AO_DATA_DIR=/Users/me/.ao/data",
			"AO_SESSION_ID=agent-orchestrator-51",
		}
		got := stripEnvKeys(in, daemonOnlyEnvKeys)
		want := []string{
			"PATH=/usr/bin",
			"AO_DATA_DIR=/Users/me/.ao/data",
			"AO_SESSION_ID=agent-orchestrator-51",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("stripEnvKeys = %v, want %v", got, want)
		}
	})

	t.Run("drops an inherited simulator so only AO decides who gets one", func(t *testing.T) {
		in := []string{"PATH=/usr/bin", "AO_SIM_UDID=C1ADF56E", "AO_SIM_DESTINATION=id=C1ADF56E"}
		got := stripEnvKeys(in, execEnvDropKeys)
		if want := []string{"PATH=/usr/bin"}; !reflect.DeepEqual(got, want) {
			t.Errorf("stripEnvKeys = %v, want %v", got, want)
		}
	})

	t.Run("drops a caller session's Claude Code and AO identity so a daemon started inside one does not hand it on", func(t *testing.T) {
		in := []string{
			"PATH=/usr/bin",
			"HOME=/Users/me",
			"ANTHROPIC_API_KEY=shell-key",
			"CLAUDE_CODE_USE_BEDROCK=1",
			"AO_DATA_DIR=/Users/me/.ao/data",
			"AO_JIRA_URL=https://jira.example",
			"CLAUDECODE=1",
			"CLAUDE_CODE_SESSION_ID=6f1c",
			"CLAUDE_CODE_CHILD_SESSION=1",
			"CLAUDE_CODE_MESSAGING_SOCKET=/tmp/claude.sock",
			"CLAUDE_CODE_MESSAGING_TOKEN=caller-token",
			"CLAUDE_CODE_ENTRYPOINT=cli",
			"CLAUDE_CODE_EXECPATH=/opt/homebrew/bin/claude",
			"CLAUDE_CODE_SESSION_ATTENDED=1",
			"CLAUDE_CODE_SESSION_KIND=bg",
			"AO_SESSION_ID=agent-orchestrator-355",
			"AO_SESSION_KIND=worker",
			"AO_PROJECT_ID=agent-orchestrator",
			"AO_ISSUE_ID=ISSUE-1",
			"AO_WORKSPACE=/Users/me/.ao/data/worktrees/x",
			"AO_CREW_ID=agent-orchestrator-355",
			"AO_CREW_DEV_ID=agent-orchestrator-355",
			"AO_CREW_QA_ID=agent-orchestrator-356",
			"AO_CHILD_WORKTREES=1",
			"AO_SCRIPTS_STORE=/Users/me/store",
			"AO_AGENT_PARENT_PID=4242",
		}
		got := stripEnvKeys(in, execEnvDropKeys)
		want := []string{
			"PATH=/usr/bin",
			"HOME=/Users/me",
			"ANTHROPIC_API_KEY=shell-key",
			"CLAUDE_CODE_USE_BEDROCK=1",
			"AO_DATA_DIR=/Users/me/.ao/data",
			"AO_JIRA_URL=https://jira.example",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("stripEnvKeys = %v, want %v", got, want)
		}
	})

	t.Run("does not mutate the input slice", func(t *testing.T) {
		in := []string{"AO_OWNER=app", "PATH=/usr/bin"}
		_ = stripEnvKeys(in, daemonOnlyEnvKeys)
		if in[0] != "AO_OWNER=app" {
			t.Errorf("input mutated: %v", in)
		}
	})

	t.Run("returns the same content when no keys match", func(t *testing.T) {
		in := []string{"PATH=/usr/bin", "HOME=/Users/me"}
		got := stripEnvKeys(in, daemonOnlyEnvKeys)
		if !reflect.DeepEqual(got, in) {
			t.Errorf("stripEnvKeys = %v, want %v", got, in)
		}
	})

	t.Run("a bare key with no '=' matches by its whole string", func(t *testing.T) {
		// os.Environ() always yields KEY=VALUE, but be explicit: "AO_OWNER" with no
		// '=' has key "AO_OWNER" and is dropped just like "AO_OWNER=app".
		in := []string{"AO_OWNER", "AO_OWNER=app", "PATH=/usr/bin"}
		got := stripEnvKeys(in, daemonOnlyEnvKeys)
		want := []string{"PATH=/usr/bin"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("stripEnvKeys = %v, want %v", got, want)
		}
	})
}
