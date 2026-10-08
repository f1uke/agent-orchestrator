package wiki

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type launchEnvAgent struct{ fakeAgent }

func (*launchEnvAgent) LaunchEnv() map[string]string {
	return map[string]string{"CLAUDE_CODE_DISABLE_AGENT_VIEW": "1"}
}

func TestStart_PaneCarriesTheAgentsLaunchEnv(t *testing.T) {
	rt := &fakeRuntime{}
	agent := &launchEnvAgent{fakeAgent{argv: []string{"/bin/claude"}}}
	svc := New(Deps{Settings: &fakeSettings{vault: t.TempDir()}, Agents: fakeResolver{agent: agent}, Runtime: rt})

	if _, err := svc.Start(context.Background(), domain.HarnessClaudeCode); err != nil {
		t.Fatal(err)
	}
	if got := rt.created[0].Env["CLAUDE_CODE_DISABLE_AGENT_VIEW"]; got != "1" {
		t.Fatalf("wiki pane env CLAUDE_CODE_DISABLE_AGENT_VIEW = %q, want 1", got)
	}
}
