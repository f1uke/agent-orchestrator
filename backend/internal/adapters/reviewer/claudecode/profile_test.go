package claudecode

import (
	"context"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReviewCommandLaunchesOnTheProfileWithTheAgentViewOff(t *testing.T) {
	agent := &captureAgent{}
	r := &Reviewer{agent: agent}

	spec, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{
		ReviewerID:    "review-w1",
		WorkspacePath: "/ws/w1",
		SettingsFile:  "/home/u/.claude/settings-omniroute.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if agent.got.SettingsFile != "/home/u/.claude/settings-omniroute.json" {
		t.Fatalf("reviewer settings file = %q, want the project profile's", agent.got.SettingsFile)
	}
	if !reflect.DeepEqual(spec.Env, map[string]string{"CLAUDE_CODE_DISABLE_AGENT_VIEW": "1"}) {
		t.Fatalf("reviewer env = %v, want CLAUDE_CODE_DISABLE_AGENT_VIEW=1", spec.Env)
	}
}

func TestNewReviewerUsesTheRealAdapterLaunchEnv(t *testing.T) {
	if got := ports.LaunchEnvOf(New().agent); got["CLAUDE_CODE_DISABLE_AGENT_VIEW"] != "1" {
		t.Fatalf("the production reviewer's agent declares no agent-view env: %v", got)
	}
}

func (a *captureAgent) LaunchEnv() map[string]string {
	return map[string]string{"CLAUDE_CODE_DISABLE_AGENT_VIEW": "1"}
}
