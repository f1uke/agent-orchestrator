package claudecode

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/toolcurate"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// toolHookPayload is the slice of Claude Code's native tool-hook payload AO
// reads. The envelope also carries transcript_path, cwd and — on PostToolUse —
// tool_response (command output, file contents); none of that is decoded here,
// and tool_input is handed straight to the curator, which whitelists it per
// tool. See toolcurate's package doc for the safety contract.
type toolHookPayload struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// DeriveActivityDetail maps a Claude Code tool hook event and its native payload
// onto the curated detail AO may transmit. ok=false when the event is not a tool
// event (it then carries no per-action detail, only the activity state that
// DeriveActivityState already reports).
//
// Claude Code installs the trio with no matcher, so every tool fires:
// PreToolUse is the "about to", and PostToolUseFailure fires INSTEAD of
// PostToolUse when the tool fails — giving three truthful, distinguishable
// moments.
//
// event is the AO hook sub-command name installed in claudeManagedHooks, NOT the
// native Claude event name. A tool hook that fired is itself a fact, so a
// missing/unparseable payload still reports the event with no detail rather than
// reporting nothing.
func DeriveActivityDetail(event string, payload []byte) (domain.ActivityDetail, bool) {
	var kind domain.ActivityEventKind
	switch event {
	case "pre-tool-use":
		kind = domain.ActivityEventToolStart
	case "post-tool-use":
		kind = domain.ActivityEventToolEnd
	case "post-tool-use-failure":
		kind = domain.ActivityEventToolFailed
	default:
		return domain.ActivityDetail{}, false
	}

	var p toolHookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return domain.ActivityDetail{Kind: kind}, true
	}
	return toolcurate.Curate(kind, p.ToolName, p.ToolInput), true
}
