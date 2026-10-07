package claudecode

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// captureAgent is a stub ports.Agent that records the LaunchConfig the reviewer
// builds, so the test asserts the reviewer's tool policy without needing the
// real claude binary on PATH.
type captureAgent struct {
	got      ports.LaunchConfig
	strategy ports.PromptDeliveryStrategy
}

func (a *captureAgent) GetConfigSpec(context.Context) (ports.ConfigSpec, error) {
	return ports.ConfigSpec{}, nil
}
func (a *captureAgent) GetLaunchCommand(_ context.Context, cfg ports.LaunchConfig) ([]string, error) {
	a.got = cfg
	return []string{"claude"}, nil
}
func (a *captureAgent) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if a.strategy == "" {
		return ports.PromptDeliveryInCommand, nil
	}
	return a.strategy, nil
}
func (a *captureAgent) GetAgentHooks(context.Context, ports.WorkspaceHookConfig) error { return nil }
func (a *captureAgent) GetRestoreCommand(context.Context, ports.RestoreConfig) ([]string, bool, error) {
	return nil, false, nil
}
func (a *captureAgent) SessionInfo(context.Context, ports.SessionRef) (ports.SessionInfo, bool, error) {
	return ports.SessionInfo{}, false, nil
}

func TestReviewCommandLaunchesReadOnlyOffBypass(t *testing.T) {
	agent := &captureAgent{}
	r := &Reviewer{agent: agent}

	if _, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{
		ReviewerID:    "review-w1",
		WorkspacePath: "/ws/w1",
		Prompt:        "review it",
		SystemPrompt:  "you are a reviewer",
	}); err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}

	// The allowlist is what enforces read-only, so it must launch in an
	// explicit non-bypass mode: bypassPermissions ignores allow/deny rules
	// entirely, and an empty mode would defer to a user's defaultMode.
	if agent.got.Permissions != ports.PermissionModeAuto {
		t.Fatalf("reviewer must launch in auto permission mode; got %q", agent.got.Permissions)
	}
	if !contains(agent.got.AllowedTools, "Read") || !contains(agent.got.AllowedTools, "Bash(ao review submit:*)") {
		t.Fatalf("allowlist missing read-only review tools: %#v", agent.got.AllowedTools)
	}
	for _, denied := range []string{"Edit", "Write", "Bash(git push:*)", "Bash(git commit:*)"} {
		if !contains(agent.got.DisallowedTools, denied) {
			t.Fatalf("disallow list missing %q: %#v", denied, agent.got.DisallowedTools)
		}
	}
}

func TestAllowlistCoversPromptRequiredPipedCommands(t *testing.T) {
	agent := &captureAgent{}
	r := &Reviewer{agent: agent}

	if _, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{
		ReviewerID:    "review-w1",
		WorkspacePath: "/ws/w1",
		Prompt:        "review it",
		SystemPrompt:  "you are a reviewer",
	}); err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}

	if !contains(agent.got.AllowedTools, "Bash(printf:*)") {
		t.Fatalf("allowlist missing printf for piped review commands: %#v", agent.got.AllowedTools)
	}

	for _, cmd := range []string{
		"printf '%s' '{ \"event\": \"COMMENT\", \"body\": \"x\" }' | gh api --method POST repos/o/r/pulls/1/reviews --input - --jq '.id'",
		"glab mr note create 42 -R group/sub/proj --file main.go --line 10 -m 'finding'",
		"printf '%s' '{ \"reviews\": [] }' | ao review submit --session sess-1 --reviews -",
	} {
		if !compoundCommandCovered(agent.got.AllowedTools, cmd) {
			t.Fatalf("allowlist does not cover prompt-required command %q with tools %#v", cmd, agent.got.AllowedTools)
		}
	}

	disallowed := "printf x | rm -rf /"
	if compoundCommandCovered(agent.got.AllowedTools, disallowed) {
		t.Fatalf("allowlist unexpectedly covers disallowed command %q with tools %#v", disallowed, agent.got.AllowedTools)
	}
}

func compoundCommandCovered(allowedTools []string, cmd string) bool {
	for _, segment := range splitPipedCommand(cmd) {
		if !bashSegmentCovered(allowedTools, segment) {
			return false
		}
	}
	return true
}

func splitPipedCommand(cmd string) []string {
	rawSegments := strings.Split(cmd, "|")
	segments := make([]string, 0, len(rawSegments))
	for _, segment := range rawSegments {
		if trimmed := strings.TrimSpace(segment); trimmed != "" {
			segments = append(segments, trimmed)
		}
	}
	return segments
}

func bashSegmentCovered(allowedTools []string, segment string) bool {
	for _, tool := range allowedTools {
		cmd, ok := strings.CutPrefix(tool, "Bash(")
		if !ok {
			continue
		}
		cmd, ok = strings.CutSuffix(cmd, ":*)")
		if !ok {
			continue
		}
		if strings.HasPrefix(segment, cmd) {
			return true
		}
	}
	return false
}

func contains(values []string, needle string) bool {
	for _, v := range values {
		if v == needle {
			return true
		}
	}
	return false
}

// Given a prompt file, a reviewer whose agent reads stdin launches with the file
// on stdin and the prompt off argv; an agent that does not read stdin keeps the
// prompt in its command.
func TestReviewCommandFeedsPromptFileOnStdin(t *testing.T) {
	inv := ports.ReviewInvocation{ReviewerID: "review-w1", Prompt: "review it", PromptFile: "/data/prompts/w1/reviewer-prompt.md"}

	agent := &captureAgent{strategy: ports.PromptDeliveryStdin}
	spec, err := (&Reviewer{agent: agent}).ReviewCommand(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	if agent.got.Prompt != "" || spec.StdinFile != inv.PromptFile {
		t.Fatalf("launch prompt = %q, stdin = %q; want none and %q", agent.got.Prompt, spec.StdinFile, inv.PromptFile)
	}

	agent = &captureAgent{}
	spec, err = (&Reviewer{agent: agent}).ReviewCommand(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	if agent.got.Prompt != "review it" || spec.StdinFile != "" {
		t.Fatalf("launch prompt = %q, stdin = %q; want the prompt in the command", agent.got.Prompt, spec.StdinFile)
	}
}
