package claudecode

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDeriveActivityDetail_ToolLifecycle(t *testing.T) {
	// A real claude-code payload shape: the hook envelope plus the native
	// tool_input, byte-identical to the transcript's tool_use.input object.
	payload := []byte(`{
		"session_id":"abc","transcript_path":"/Users/someone/.claude/projects/x/y.jsonl",
		"cwd":"/w","hook_event_name":"PreToolUse","tool_name":"Bash",
		"tool_input":{"command":"pnpm test","description":"Running the test suite"}
	}`)
	cases := []struct {
		event string
		want  domain.ActivityEventKind
	}{
		{"pre-tool-use", domain.ActivityEventToolStart},
		{"post-tool-use", domain.ActivityEventToolEnd},
		{"post-tool-use-failure", domain.ActivityEventToolFailed},
	}
	for _, tc := range cases {
		t.Run(tc.event, func(t *testing.T) {
			got, ok := DeriveActivityDetail(tc.event, payload)
			if !ok {
				t.Fatalf("%s must carry detail", tc.event)
			}
			if got.Kind != tc.want {
				t.Errorf("Kind = %q, want %q", got.Kind, tc.want)
			}
			if got.Tool != "Bash" || got.Text != "Running the test suite" {
				t.Errorf("detail = %+v, want the Bash description", got)
			}
		})
	}
}

func TestDeriveActivityDetail_NonToolEventsCarryNoDetail(t *testing.T) {
	for _, event := range []string{"user-prompt-submit", "stop", "notification", "session-end", "session-start", ""} {
		if _, ok := DeriveActivityDetail(event, []byte(`{"tool_name":"Bash"}`)); ok {
			t.Errorf("%s must not produce tool detail", event)
		}
	}
}

// A tool AO does not curate still reports that SOMETHING is happening — the hook
// firing is itself a fact — but contributes no detail at all.
func TestDeriveActivityDetail_UnknownToolDegradesSilently(t *testing.T) {
	got, ok := DeriveActivityDetail("pre-tool-use",
		[]byte(`{"tool_name":"mcp__internal__deploy","tool_input":{"description":"Deploying prod","token":"s3cr3t"}}`))
	if !ok {
		t.Fatal("an unknown tool must still report the event")
	}
	if got.Kind != domain.ActivityEventToolStart {
		t.Errorf("Kind = %q, want tool_start", got.Kind)
	}
	if got.Tool != "" || got.Target != "" || got.Text != "" {
		t.Errorf("an uncurated tool must contribute nothing, got %+v", got)
	}
}

func TestDeriveActivityDetail_MissingOrBrokenPayload(t *testing.T) {
	for _, payload := range []string{"", "{}", "not json", `{"tool_name":""}`} {
		got, ok := DeriveActivityDetail("post-tool-use", []byte(payload))
		if !ok {
			t.Fatalf("%q: the hook still fired, so the event stands", payload)
		}
		if got.Kind != domain.ActivityEventToolEnd {
			t.Errorf("%q: Kind = %q, want tool_end", payload, got.Kind)
		}
		if got.Tool != "" || got.Text != "" || got.Target != "" {
			t.Errorf("%q: want no detail, got %+v", payload, got)
		}
	}
}

// The single most important test in this package: a Write tool's file body
// arrives in tool_input.content, and a PostToolUse also carries tool_response.
// Neither may appear in the derived detail.
func TestDeriveActivityDetail_WriteContentsNeverEscape(t *testing.T) {
	const secret = "CANARY-FILE-BODY-4c81"
	payload := []byte(`{
		"hook_event_name":"PostToolUse","tool_name":"Write",
		"tool_input":{"file_path":"/Users/someone/private/.env","content":"OPENAI_KEY=` + secret + `"},
		"tool_response":{"filePath":"/Users/someone/private/.env","content":"OPENAI_KEY=` + secret + `"}
	}`)
	got, ok := DeriveActivityDetail("post-tool-use", payload)
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
	if strings.Contains(string(blob), "/Users/someone") {
		t.Fatalf("path leak: %s", blob)
	}
	if got.Target != ".env" {
		t.Errorf("Target = %q, want the base name only", got.Target)
	}
}
