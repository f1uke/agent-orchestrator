package opencode

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The plugin's tool events keep the session demonstrably busy, exactly like
// claude-code's per-tool hooks do.
func TestDeriveActivityState_ToolEventsReportActive(t *testing.T) {
	for _, event := range []string{"tool-start", "tool-end", "tool-failed"} {
		got, ok := DeriveActivityState(event, nil)
		if !ok || got != domain.ActivityActive {
			t.Errorf("%s = (%q, %v), want (active, true)", event, got, ok)
		}
	}
}

func TestDeriveActivityDetail_ToolLifecycle(t *testing.T) {
	cases := []struct {
		event string
		want  domain.ActivityEventKind
	}{
		{"tool-start", domain.ActivityEventToolStart},
		{"tool-end", domain.ActivityEventToolEnd},
		{"tool-failed", domain.ActivityEventToolFailed},
	}
	for _, tc := range cases {
		t.Run(tc.event, func(t *testing.T) {
			got, ok := DeriveActivityDetail(tc.event, []byte(`{"session_id":"ses-1","tool":"bash"}`))
			if !ok {
				t.Fatalf("%s must carry detail", tc.event)
			}
			if got.Kind != tc.want {
				t.Errorf("Kind = %q, want %q", got.Kind, tc.want)
			}
			// opencode names its tools lower-case; AO emits its canonical name.
			if got.Tool != "Bash" {
				t.Errorf("Tool = %q, want Bash", got.Tool)
			}
			// The plugin ships the tool NAME only — AO has not verified the shape
			// of opencode's tool input, so no field of it is transmitted.
			if got.Target != "" || got.Text != "" {
				t.Errorf("opencode detail is name-only, got %+v", got)
			}
		})
	}
}

func TestDeriveActivityDetail_NonToolEventsCarryNoDetail(t *testing.T) {
	for _, event := range []string{"session-start", "user-prompt-submit", "stop", ""} {
		if _, ok := DeriveActivityDetail(event, []byte(`{"tool":"bash"}`)); ok {
			t.Errorf("%s must not produce tool detail", event)
		}
	}
}

// Even though the plugin only sends a name, assert nothing else in a payload can
// ride along into the feed.
func TestDeriveActivityDetail_PayloadFieldsNeverEscape(t *testing.T) {
	const secret = "CANARY-OPENCODE-77b2"
	got, ok := DeriveActivityDetail("tool-end", []byte(`{
		"session_id":"ses-1","tool":"bash",
		"input":{"command":"deploy --token=`+secret+`"},"output":"`+secret+`","title":"`+secret+`"
	}`))
	if !ok {
		t.Fatal("expected detail")
	}
	blob, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), secret) {
		t.Fatalf("SECRET LEAK: %s", blob)
	}
}

func TestDeriveActivityDetail_UnknownToolDegradesSilently(t *testing.T) {
	got, ok := DeriveActivityDetail("tool-start", []byte(`{"tool":"some-mcp-thing"}`))
	if !ok {
		t.Fatal("the hook fired, so the event stands")
	}
	if got.Tool != "" {
		t.Errorf("Tool = %q, want empty for an uncurated tool", got.Tool)
	}
}
