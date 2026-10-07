package daemon

import (
	"encoding/json"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
)

// The payload is the one the sessions change trigger writes (migration 0001).
func TestSessionEnded(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    cdc.Event
		want bool
	}{
		{"terminated", cdc.Event{Type: cdc.EventSessionUpdated, SessionID: "mer-1", Payload: json.RawMessage(`{"id":"mer-1","activity":"idle","isTerminated":true}`)}, true},
		{"still running", cdc.Event{Type: cdc.EventSessionUpdated, SessionID: "mer-1", Payload: json.RawMessage(`{"id":"mer-1","activity":"active","isTerminated":false}`)}, false},
		{"created", cdc.Event{Type: cdc.EventSessionCreated, SessionID: "mer-1", Payload: json.RawMessage(`{"isTerminated":true}`)}, false},
		{"no session", cdc.Event{Type: cdc.EventSessionUpdated, Payload: json.RawMessage(`{"isTerminated":true}`)}, false},
	} {
		if got := sessionEnded(tc.e); got != tc.want {
			t.Errorf("%s: sessionEnded = %v, want %v", tc.name, got, tc.want)
		}
	}
}
