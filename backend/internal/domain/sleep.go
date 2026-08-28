package domain

// WHY A SESSION IS ASLEEP, AND WHAT WOKE IT.
//
// `is_suspended` is one boolean carrying several different facts, and the reason
// is the ANNOTATION that tells them apart. It is deliberately not a second state:
// every reader of `is_suspended` except the view - the message queue, the reaper,
// boot reconciliation, the shutdown sweep, status derivation, Awake() itself -
// wants the same answer for all of them ("there is no process here").
//
// ONE REASON WAS DELETED: "not its turn". It existed because a crew shared one
// worktree and only ONE member could be awake in it, so a member could be asleep
// purely because the other one was running - and looking at its card had to leave
// it that way, since deciding which agent runs is a decision, not a side effect
// of opening a card. Both members run at the same time now (crew_slot.go), so
// nothing sleeps for turn reasons, nothing writes that value any more, and no
// card has to refuse to wake.
//
// Rows written before that still carry the string, and the column's CHECK still
// permits it - deliberately, because rewriting history to say a session slept for
// a reason it did not would be worse than a value nothing produces. It is not a
// constant here, so it falls through to the default: opening it resumes it, which
// is now the right answer for every kind of sleep.
//
// A crew member that has NEVER RUN is a real state and it is not a reason: qa is
// created as a row and stays asleep until somebody starts it. That is read off
// the row that already says it - no runtime handle, so `crew.hasRun` is false -
// rather than off a new sleep reason, which would need the sessions table rebuilt
// to widen a CHECK constraint for a fact already on the wire.

// SleepReason says why a suspended session is asleep.
type SleepReason string

// The reasons AO puts a session to sleep. The zero value is the empty string,
// meaning NOT RECORDED - every row written before this field existed. Unknown
// behaves exactly as it did before this change (a view resumes it), so only
// SleepReasonTurn moves any behaviour at all.
const (
	// SleepReasonIdle: the idle sweep paused it to free machine resources, or
	// boot reconciliation found it idle past the same window. Opening it resumes
	// it in place - the behaviour the human relies on daily.
	SleepReasonIdle SleepReason = "idle"
	// SleepReasonMerged: a keep-warm worker whose PR merged, parked in place with
	// the "Merged - open to continue" affordance. Opening it resumes it, which is
	// what that affordance promises.
	SleepReasonMerged SleepReason = "merged"
	// SleepReasonUndelivered: an agent ended its OWN session while holding work
	// that had reached nobody - no pull request had ever been opened from the
	// worktree it still owns. Terminating there would file the task as finished,
	// so the row is parked instead: the card stays on the board reading
	// needs_input, the worktree is kept, and opening it resumes the agent into
	// the tree its work is still sitting in.
	SleepReasonUndelivered SleepReason = "undelivered"
)

// WokenBy names what brought a suspended session's runtime back. It exists
// because change_log recorded that `is_suspended` flipped but not who flipped
// it, so an unexplained wake could only be reasoned about from timestamps.
type WokenBy string

// The things that can wake a sleeping session. The zero value means the session
// was not asleep (a fresh spawn) or predates the field.
const (
	// WokenByView: the desktop app opened the session's view or placed it in a
	// split pane. Automatic - nobody pressed anything. This is the value that
	// would have named the culprit in the incident.
	WokenByView WokenBy = "view"
	// WokenByWake: somebody asked for it by name - `ao crew wake`, or the API it
	// calls.
	WokenByWake WokenBy = "wake"
	// WokenByRestore: the user restored/restarted the session.
	WokenByRestore WokenBy = "restore"
	// WokenByBoot: the daemon's boot restore pass brought it back.
	WokenByBoot WokenBy = "boot"
	// WokenBySpawn: a first launch. Never recorded on the row (a spawning session
	// was not asleep); it exists so every call site names itself.
	WokenBySpawn WokenBy = "spawn"
)
