package activitydispatch

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Every deriver key must be a known harness name: SupportsHarness equates the
// two, so a token that drifts from its harness constant would silently report
// the harness as hook-less.
func TestDeriverTokensAreKnownHarnesses(t *testing.T) {
	for token := range Derivers {
		if !domain.AgentHarness(token).IsKnown() {
			t.Errorf("deriver token %q is not a known AgentHarness", token)
		}
	}
}

func TestSupportsHarness(t *testing.T) {
	for _, h := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode, domain.HarnessOpenCode} {
		if !SupportsHarness(h) {
			t.Errorf("SupportsHarness(%q) = false, want true", h)
		}
	}
	// Harnesses whose adapters install no hooks must read as unsupported so
	// their silence never derives no_signal.
	for _, h := range []domain.AgentHarness{domain.HarnessAmp, domain.HarnessAider, domain.HarnessCrush, domain.AgentHarness("")} {
		if SupportsHarness(h) {
			t.Errorf("SupportsHarness(%q) = true, want false", h)
		}
	}
}

// A detail deriver is only reachable through the same hook call that reports an
// activity state, so a harness registered for detail but not for state would
// have its curated detail silently dropped.
func TestDetailDeriverTokensAlsoHaveStateDerivers(t *testing.T) {
	for token := range DetailDerivers {
		if !domain.AgentHarness(token).IsKnown() {
			t.Errorf("detail deriver token %q is not a known AgentHarness", token)
		}
		if _, ok := Derivers[token]; !ok {
			t.Errorf("detail deriver %q has no activity-state deriver, so its detail can never be reported", token)
		}
	}
}

func TestDeriveDetail(t *testing.T) {
	cases := []struct {
		name    string
		agent   string
		event   string
		payload string
		wantOK  bool
		want    domain.ActivityDetail
	}{
		{
			name: "claude-code curates a Bash description", agent: "claude-code", event: "pre-tool-use",
			payload: `{"tool_name":"Bash","tool_input":{"command":"pnpm test","description":"Running the test suite"}}`,
			wantOK:  true,
			want:    domain.ActivityDetail{Kind: domain.ActivityEventToolStart, Tool: "Bash", Text: "Running the test suite"},
		},
		{
			name: "opencode reports a tool name only", agent: "opencode", event: "tool-end",
			payload: `{"tool":"grep"}`, wantOK: true,
			want: domain.ActivityDetail{Kind: domain.ActivityEventToolEnd, Tool: "Grep"},
		},
		{
			name: "agy reports a completion only", agent: "agy", event: "after-tool",
			payload: `{"tool_name":"Edit"}`, wantOK: true,
			want: domain.ActivityDetail{Kind: domain.ActivityEventToolEnd, Tool: "Edit"},
		},
		{
			name: "a harness with no detail deriver degrades silently", agent: "codex", event: "user-prompt-submit",
			payload: `{"tool_name":"Bash"}`,
		},
		{
			name: "an unregistered agent token reports nothing", agent: "aider", event: "pre-tool-use",
			payload: `{"tool_name":"Bash"}`,
		},
		{
			name: "a non-tool event reports nothing", agent: "claude-code", event: "stop",
			payload: `{}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DeriveDetail(tc.agent, tc.event, []byte(tc.payload))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Errorf("detail = %+v, want %+v", got, tc.want)
			}
		})
	}
}
