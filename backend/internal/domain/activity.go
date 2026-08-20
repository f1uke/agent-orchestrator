package domain

import "time"

// ActivityState is how busy the agent is, reported via the agent's CLI hook
// callbacks (see docs/agent/README.md), not inferred from transcript/JSONL
type ActivityState string

// Activity states. WaitingInput is sticky (see IsSticky).
//
// WaitingInput and Parked are deliberately distinct, because "the agent is not
// working" splits into two situations that must be treated differently:
//
//   - WaitingInput: a PROMPT IS OPEN in the pane and the agent is blocked on the
//     human answering it. Every adapter reports this from a permission callback
//     (claude-code's Notification{permission_prompt}, everyone else's
//     permission-request). Nothing may be typed at such a pane: the keystrokes go
//     into the dialog and the trailing Enter can ANSWER it.
//   - Parked: the turn is over and the agent is sitting at an ordinary, empty
//     prompt. Nobody is blocked and nothing is open; this is precisely the state
//     in which an agent most needs to be told its CI went red. Injecting here is
//     what a nudge is for.
const (
	ActivityActive       ActivityState = "active"
	ActivityIdle         ActivityState = "idle"
	ActivityWaitingInput ActivityState = "waiting_input"
	ActivityParked       ActivityState = "parked"
	ActivityExited       ActivityState = "exited"
)

// IsSticky reports whether an activity state must NOT be aged/demoted by the
// passage of time (a paused agent is still paused until a new signal says so).
//
// Parked is deliberately NOT sticky. Stickiness here also tells the reaper that a
// session with a dead runtime probe still counts as recently active, which is
// right for a human-blocking prompt but would strand a parked session whose tmux
// has died. Nothing needs parked to be sticky: the status deriver reads the state
// itself rather than ageing its timestamp, so parked already means "your turn"
// the moment it is reported.
func (a ActivityState) IsSticky() bool {
	return a == ActivityWaitingInput
}

// IsListening reports whether a message typed at this session's pane right now
// would reach the AGENT rather than something else. It is false only for
// WaitingInput, where an open prompt would swallow the keystrokes; a parked or
// idle agent is listening, and an active one is mid-turn but still reading its
// input line.
//
// Callers that must not deliver a message use this instead of comparing against
// WaitingInput themselves, so the rule lives in one place.
func (a ActivityState) IsListening() bool {
	return a != ActivityWaitingInput
}

// Activity captures the persisted activity reading: the state and when it was
// last observed.
type Activity struct {
	State          ActivityState `json:"state"`
	LastActivityAt time.Time     `json:"lastActivityAt"`
}
