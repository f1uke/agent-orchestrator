package activitydispatch

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Every end-reason deriver must belong to a harness that also derives activity
// state: the reason rides the same hook call as the exited signal, so a deriver
// registered without one could never be reached.
func TestEndReasonDeriverTokensAlsoHaveStateDerivers(t *testing.T) {
	for token := range EndReasonDerivers {
		if !domain.AgentHarness(token).IsKnown() {
			t.Errorf("end-reason deriver token %q is not a known AgentHarness", token)
		}
		if _, ok := Derivers[token]; !ok {
			t.Errorf("end-reason deriver %q has no activity-state deriver, so its reason can never be reported", token)
		}
	}
}

func TestDeriveEndReason(t *testing.T) {
	if got, ok := DeriveEndReason("claude-code", "session-end", []byte(`{"reason":"logout"}`)); !ok || got != "logout" {
		t.Errorf("claude-code session-end = (%q, %v), want (\"logout\", true)", got, ok)
	}
	// A harness with no end-reason deriver still ends: the caller records the
	// agent as the source with an unknown cause.
	if got, ok := DeriveEndReason("codex", "session-end", []byte(`{"reason":"logout"}`)); ok {
		t.Errorf("codex = (%q, true), want ok=false", got)
	}
}

// The reason crosses a process boundary into the daemon, so it is bounded to a
// short token shape. A harness that ever put a path, a prompt or a secret in
// that field must not be able to smuggle it through — and an unrecognised but
// well-shaped token from a newer harness must still get through, because
// recording the real reason beats recording "unknown".
func TestDeriveEndReason_RejectsAnythingThatIsNotAShortToken(t *testing.T) {
	rejected := []string{
		`{"reason":"/Users/someone/.claude/projects/p/t.jsonl"}`,
		`{"reason":"OPENAI_KEY=sk-canary"}`,
		`{"reason":"the user pressed ctrl-c twice"}`,
		`{"reason":"` + strings.Repeat("x", 200) + `"}`,
	}
	for _, payload := range rejected {
		if got, ok := DeriveEndReason("claude-code", "session-end", []byte(payload)); ok {
			t.Errorf("DeriveEndReason(%s) = (%q, true), want ok=false", payload, got)
		}
	}
	// A token AO has never seen but which is shaped like a reason still passes.
	if got, ok := DeriveEndReason("claude-code", "session-end", []byte(`{"reason":"quota_exhausted"}`)); !ok || got != "quota_exhausted" {
		t.Errorf("an unfamiliar token = (%q, %v), want it forwarded", got, ok)
	}
}
