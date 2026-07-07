package claudecode

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// DeriveActivityState maps a Claude Code hook event (and its native stdin
// payload) onto an AO activity state. The bool is false when the event carries
// no activity signal — e.g. SessionStart (metadata only, v1), a Notification
// type we don't track, or a SessionEnd reason that doesn't actually end the AO
// session — in which case the caller reports nothing.
//
// event is the AO hook sub-command name installed in claudeManagedHooks
// ("user-prompt-submit", "stop", "notification", "session-end", ...), NOT the
// native Claude event name. Keeping this beside hooks.go means the events AO
// installs and what they mean live in one place.
func DeriveActivityState(event string, payload []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit":
		return domain.ActivityActive, true
	case "pre-tool-use", "post-tool-use", "post-tool-use-failure":
		// A tool is about to run / just ran, so the agent is demonstrably busy.
		// These fire for tool calls inside Task sub-agents too, which is what
		// keeps a session "working" during a long sub-agent run. They are also
		// the signal that clears a stale waiting_input: a permission prompt
		// answered directly in the TUI produces no hook of its own, so without
		// these the sticky waiting_input outlives the approval indefinitely.
		// A tool that fails (e.g. a nonzero bash exit) fires PostToolUseFailure
		// INSTEAD of PostToolUse, so liveness needs both completion variants.
		// Ordering is safe for real prompts: PreToolUse completes before the
		// permission check, so its "active" always lands before the prompt's
		// Notification sets waiting_input.
		return domain.ActivityActive, true
	case "stop":
		// End of a turn: the agent is idle but alive (not exited). This is how a
		// recap/auto-summary turn ends too — informational, not a request for
		// input. A sustained idle is promoted to needs-input by the status deriver
		// (waitingInputGrace), which correctly keeps its hands off an open PR; a
		// later Notification(idle_prompt) carries no new signal (see below).
		return domain.ActivityIdle, true
	case "notification":
		return notificationState(payload)
	case "session-end":
		return sessionEndState(payload)
	default:
		return "", false
	}
}

// notificationState reports waiting_input only for a permission_prompt: a
// pending tool-permission decision genuinely blocks the agent on the human, and
// waiting_input is sticky so it survives until answered.
//
// idle_prompt is deliberately NOT treated as waiting_input. It only means the
// agent has been sitting idle at the prompt — the same state a Stop hook already
// recorded, and exactly what a recap/auto-summary turn leaves behind. Promoting
// it to the sticky waiting_input made an idle, finished session look like it was
// "requesting input": it short-circuited the status deriver ahead of the open-PR
// check and demoted a ready-to-merge PR back to needs_input on every recap.
// Leaving it as plain idle lets the deriver's sustained-idle promotion
// (waitingInputGrace) surface "your turn" for sessions with no PR, while an open
// PR keeps its pipeline status. Other types (auth_success, elicitation_*) carry
// no activity meaning, as does a malformed payload.
func notificationState(payload []byte) (domain.ActivityState, bool) {
	var p struct {
		NotificationType string `json:"notification_type"`
	}
	_ = json.Unmarshal(payload, &p)
	switch p.NotificationType {
	case "permission_prompt":
		return domain.ActivityWaitingInput, true
	default:
		return "", false
	}
}

// sessionEndState reports exited for reasons that actually end the session.
// clear/resume keep the same AO session alive (a new native session continues
// in the worktree), so they report nothing. Any other reason — logout,
// prompt_input_exit, bypass_permissions_disabled, other, or an absent/unknown
// reason on a SessionEnd that did fire — is treated as a real exit. SessionEnd
// is not guaranteed on crash/SIGKILL, so the reaper remains the backstop; both
// paths guard on IsTerminated, so whichever lands first wins.
func sessionEndState(payload []byte) (domain.ActivityState, bool) {
	var p struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(payload, &p)
	switch p.Reason {
	case "clear", "resume":
		return "", false
	default:
		return domain.ActivityExited, true
	}
}
