package claudecode

import (
	"encoding/json"
	"regexp"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/fingerprint"
)

// transcriptRefPayload is the slice of a Claude Code hook envelope learning
// capture reads: which conversation file the session is writing, and - on
// UserPromptSubmit only - the submitted prompt, which is reduced to a
// fingerprint right here, inside the hook process, and never leaves it as text.
type transcriptRefPayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Prompt         string `json:"prompt"`
}

// claudeSessionIDRE bounds the native id to the UUID shape Claude Code uses.
var claudeSessionIDRE = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)

// TranscriptRef reads the conversation file and, on a prompt submit, the
// prompt's fingerprint from a hook envelope. It answers only for the low-rate
// events - start, prompt submit, stop, end - so a tool-heavy turn does not post
// once per tool call. ok=false when the event is not one of those or the
// envelope names no transcript.
func TranscriptRef(event string, payload []byte) (domain.HookTranscriptRef, bool) {
	switch event {
	case "session-start", "user-prompt-submit", "stop", "session-end":
	default:
		return domain.HookTranscriptRef{}, false
	}
	var p transcriptRefPayload
	if err := json.Unmarshal(payload, &p); err != nil || p.TranscriptPath == "" {
		return domain.HookTranscriptRef{}, false
	}
	ref := domain.HookTranscriptRef{TranscriptPath: p.TranscriptPath}
	if claudeSessionIDRE.MatchString(p.SessionID) {
		ref.ClaudeSessionID = p.SessionID
	}
	if event == "user-prompt-submit" && p.Prompt != "" {
		ref.PromptSHA256, ref.PromptBytes = fingerprint.Of(p.Prompt)
	}
	return ref, true
}
