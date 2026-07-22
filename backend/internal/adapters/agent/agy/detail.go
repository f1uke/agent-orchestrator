package agy

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/toolcurate"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// toolHookPayload is the only slice of Agy's AfterTool payload AO reads. Agy has
// no before-tool hook and AO has not verified the payload's shape against the
// harness, so nothing but a tool NAME is read — and the curator emits its own
// canonical name, never the string from the payload.
type toolHookPayload struct {
	ToolName string `json:"tool_name"`
	AltName  string `json:"toolName"`
}

// DeriveActivityDetail maps an Agy hook event onto curated detail. Only
// AfterTool carries any, and it is a completion: Agy fires nothing before a
// tool, so the feed can never say "about to" for this harness.
func DeriveActivityDetail(event string, payload []byte) (domain.ActivityDetail, bool) {
	if event != "after-tool" {
		return domain.ActivityDetail{}, false
	}
	kind := domain.ActivityEventToolEnd

	var p toolHookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return domain.ActivityDetail{Kind: kind}, true
	}
	if detail := toolcurate.CurateName(kind, p.ToolName); detail.Tool != "" {
		return detail, true
	}
	return toolcurate.CurateName(kind, p.AltName), true
}
