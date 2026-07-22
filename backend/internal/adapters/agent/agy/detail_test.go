package agy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Agy fires AfterTool only — there is no "about to" event — so the feed reports
// a completion and nothing else.
func TestDeriveActivityDetail_AfterToolIsACompletion(t *testing.T) {
	got, ok := DeriveActivityDetail("after-tool", []byte(`{"tool_name":"read_file","toolName":"Read"}`))
	if !ok {
		t.Fatal("after-tool must carry detail")
	}
	if got.Kind != domain.ActivityEventToolEnd {
		t.Errorf("Kind = %q, want tool_end", got.Kind)
	}
	if got.Tool != "Read" {
		t.Errorf("Tool = %q, want Read", got.Tool)
	}
	if got.Target != "" || got.Text != "" {
		t.Errorf("agy detail is name-only, got %+v", got)
	}
}

func TestDeriveActivityDetail_NonToolEventsCarryNoDetail(t *testing.T) {
	for _, event := range []string{"session-start", "session-end", "before-agent", "after-agent", ""} {
		if _, ok := DeriveActivityDetail(event, []byte(`{"tool_name":"Read"}`)); ok {
			t.Errorf("%s must not produce tool detail", event)
		}
	}
}

// Agy's AfterTool payload shape is not verified against the harness, so nothing
// but a whitelisted tool NAME may ever be read out of it.
func TestDeriveActivityDetail_PayloadFieldsNeverEscape(t *testing.T) {
	const secret = "CANARY-AGY-31ac"
	got, ok := DeriveActivityDetail("after-tool", []byte(`{
		"tool_name":"Write","args":{"content":"`+secret+`"},"result":"`+secret+`"
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

func TestDeriveActivityDetail_UnnamedToolStillReportsTheEvent(t *testing.T) {
	got, ok := DeriveActivityDetail("after-tool", []byte(`{}`))
	if !ok {
		t.Fatal("the hook fired, so the event stands")
	}
	if got.Kind != domain.ActivityEventToolEnd || got.Tool != "" {
		t.Errorf("detail = %+v, want a bare tool_end", got)
	}
}
