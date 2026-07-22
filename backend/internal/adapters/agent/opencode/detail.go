package opencode

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/toolcurate"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// toolHookPayload is the whole of what the plugin sends for a tool event: the
// opencode session id and the tool NAME. The plugin deliberately ships no part
// of the tool's input or output — AO has not verified the shape of opencode's
// tool part, and an unverified payload must not cross the process boundary at
// all (see assets/ao-activity.ts and toolcurate's package doc).
type toolHookPayload struct {
	Tool string `json:"tool"`
}

// DeriveActivityDetail maps an opencode plugin tool event onto curated detail.
// ok=false for the non-tool events, which carry only an activity state.
//
// opencode names its tools lower-case ("bash", "read"); the curator emits AO's
// canonical name, and a tool outside the whitelist contributes nothing.
func DeriveActivityDetail(event string, payload []byte) (domain.ActivityDetail, bool) {
	var kind domain.ActivityEventKind
	switch event {
	case "tool-start":
		kind = domain.ActivityEventToolStart
	case "tool-end":
		kind = domain.ActivityEventToolEnd
	case "tool-failed":
		kind = domain.ActivityEventToolFailed
	default:
		return domain.ActivityDetail{}, false
	}

	var p toolHookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return domain.ActivityDetail{Kind: kind}, true
	}
	return toolcurate.CurateName(kind, p.Tool), true
}
