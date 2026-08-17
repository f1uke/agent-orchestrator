package claudecode

import "testing"

// The harness's own end reason is the only account of an exit AO did not order,
// so it has to survive the hook that reports it rather than being read once for
// the state decision and dropped.
func TestSessionEndReason_ExtractsTheHarnessReason(t *testing.T) {
	got, ok := SessionEndReason("session-end", []byte(`{"reason":"prompt_input_exit","transcript_path":"/Users/someone/.claude/x.jsonl"}`))
	if !ok || got != "prompt_input_exit" {
		t.Fatalf("SessionEndReason = (%q, %v), want (\"prompt_input_exit\", true)", got, ok)
	}
}

// Only the ending carries an end reason. Reading one off any other callback
// would attach a cause to a session that has not ended.
func TestSessionEndReason_OtherEventsCarryNone(t *testing.T) {
	for _, event := range []string{"stop", "pre-tool-use", "notification", "user-prompt-submit"} {
		if got, ok := SessionEndReason(event, []byte(`{"reason":"other"}`)); ok {
			t.Errorf("SessionEndReason(%q) = (%q, true), want ok=false", event, got)
		}
	}
}

// A SessionEnd that reports nothing, or malformed JSON, still ended the session:
// the caller records the ending with an unknown cause rather than nothing at all.
func TestSessionEndReason_MissingOrMalformedReasonIsEmpty(t *testing.T) {
	for _, payload := range []string{`{}`, `not json`, `{"reason":""}`} {
		got, ok := SessionEndReason("session-end", []byte(payload))
		if !ok || got != "" {
			t.Errorf("SessionEndReason(%q) = (%q, %v), want (\"\", true)", payload, got, ok)
		}
	}
}
