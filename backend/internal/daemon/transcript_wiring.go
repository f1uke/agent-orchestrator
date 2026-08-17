package daemon

// This file wires the transcript locator the lifecycle reducer uses when it
// records HOW a session ended. Lifecycle is a pure fact layer and transcript
// layout is harness-specific, so the harness knowledge lives here rather than
// leaking into the reducer.

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// locateTranscript returns the agent transcript for a session, or "" when there
// is none AO can find. It is called once per terminal transition, so the stat it
// does costs nothing on the lifecycle path.
//
// Only claude-code writes a transcript AO knows how to locate; every other
// harness records an ending with no transcript pointer, which is the honest
// answer rather than a path that resolves to nothing. Where a session has more
// than one candidate transcript (the native id and the id AO pins both resolve),
// the first is the newest candidate.
func locateTranscript(rec domain.SessionRecord) string {
	if rec.Harness != domain.HarnessClaudeCode {
		return ""
	}
	paths := claudecode.TranscriptPaths(rec.Metadata.WorkspacePath, string(rec.ID), rec.Metadata.AgentSessionID)
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}
