package claudecode

import "encoding/json"

// SessionEndReason extracts the harness's own reason for ending a session, so
// AO can record WHY a session it did not terminate stopped. Claude Code reports
// one on SessionEnd — "prompt_input_exit", "logout", "clear", "other", and
// whatever it grows next.
//
// This is deliberately separate from sessionEndState, which reads the same field
// only to decide whether the ending is one that ends the AO session at all.
// Reading it twice for two different questions is cheaper than conflating them:
// a "clear" reports no activity state and therefore never reaches this, while
// every ending that DOES terminate carries its reason onto the record.
//
// ok=false means the event is not an ending. An ending with no reason (absent,
// empty, or a payload that will not parse) is (\"\", true): the session really
// did end, and the caller records that with an unknown cause rather than
// pretending nothing happened.
func SessionEndReason(event string, payload []byte) (string, bool) {
	if event != "session-end" {
		return "", false
	}
	var p struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(payload, &p)
	return p.Reason, true
}
