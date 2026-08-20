package claudecode

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// NestedWorktreeDenial reports whether a Claude Code hook callback would move an
// AO worker's same-task child outside the worker's existing worktree, and the
// reason to hand back to the agent.
//
// event is the AO hook sub-command name, as in DeriveActivityState. Only
// pre-tool-use can be denied: it is the one callback that runs BEFORE the tool,
// so it is the last point at which the wrong checkout can still be prevented.
//
// The caller scopes this decision to AO worker sessions; this parser deliberately
// knows nothing about process environment or session kinds.
func NestedWorktreeDenial(event string, payload []byte) (bool, string) {
	if event != "pre-tool-use" {
		return false, ""
	}
	var p struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Isolation string `json:"isolation"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return false, ""
	}
	switch p.ToolName {
	case "EnterWorktree":
		return true, "This AO worker already has an isolated worktree. Keep same-task child work in the current AO worktree and do not call EnterWorktree."
	case "Agent":
		if p.ToolInput.Isolation == "worktree" {
			return true, "This AO worker already has an isolated worktree. Launch the child without worktree isolation so its edits stay in the current AO worktree."
		}
	}
	return false, ""
}

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
		// (waitingInputGrace); a later Notification(idle_prompt) says the same thing
		// outright and reports parked (see below).
		return domain.ActivityIdle, true
	case "notification":
		return notificationState(payload)
	case "session-end":
		return sessionEndState(payload)
	default:
		return "", false
	}
}

// notificationState splits Claude Code's Notification into the two states it
// actually carries.
//
// permission_prompt is waiting_input: a pending tool-permission decision
// genuinely blocks the agent on the human, a dialog is open in the pane, and
// waiting_input is sticky so it survives until answered.
//
// idle_prompt is parked. It means the agent has settled at an ordinary, empty
// prompt with the turn over — nothing is open and nothing is blocked. It used to
// map to waiting_input, which was wrong twice over: it made a finished session
// look like it was "requesting input" (short-circuiting the status deriver ahead
// of the open-PR check, demoting a ready-to-merge PR back to needs_input on every
// recap), and it put the session behind the "do not type at this pane" bar that
// waiting_input carries, so CI/review/conflict nudges were dropped for a session
// that was perfectly able to receive them. It was then mapped to nothing at all,
// which fixed the status regression by throwing the signal away. parked keeps the
// signal AND keeps it out of waiting_input's way: the deriver reads it as the
// sustained idle it is, and nudges flow.
//
// Other types (auth_success, elicitation_*) carry no activity meaning, as does a
// malformed payload.
func notificationState(payload []byte) (domain.ActivityState, bool) {
	var p struct {
		NotificationType string `json:"notification_type"`
	}
	_ = json.Unmarshal(payload, &p)
	switch p.NotificationType {
	case "permission_prompt":
		return domain.ActivityWaitingInput, true
	case "idle_prompt":
		return domain.ActivityParked, true
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
