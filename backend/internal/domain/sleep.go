package domain

// WHY A SESSION IS ASLEEP, AND WHAT WOKE IT.
//
// `is_suspended` is one boolean that was carrying two different facts:
//
//  1. PAUSED TO FREE RESOURCES - the idle sweep tore the tmux down. Looking at
//     the session should bring it back, and it always has.
//  2. NOT ITS TURN - a crew member released by #225's ReleaseCrewSlot, or born
//     asleep waiting for the baton. Looking at it must NOT bring it back: only
//     one member of a crew may run in their shared worktree, and deciding which
//     one is a decision, not a side effect of opening a card.
//
// The daemon could not tell them apart, so the user-open hook resumed either -
// which is how a qa nobody had woken was found running twelve seconds after its
// dev's PR merged.
//
// The fix is an ANNOTATION on the existing state rather than a new state, and
// that is a deliberate difference from #222 (which split `waiting_input` in two).
// There, several consumers needed different answers for the two meanings. Here
// exactly ONE does: the view. Every other reader of `is_suspended` - the message
// queue, the reaper, boot reconciliation, the shutdown sweep, status derivation,
// and Awake() itself - wants the same answer for both ("there is no process
// here"), and Awake() must keep meaning precisely what it means to #225's
// exclusion. A second boolean state would have forced every one of them to learn
// a distinction it does not care about.

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
	// SleepReasonTurn: it is a crew member and the turn is not its own. It was
	// either born asleep at crew formation or stood down so the other member
	// could run. Only an explicit wake (`ao crew wake`, the baton bar's button)
	// may take the turn; a view may not.
	SleepReasonTurn SleepReason = "turn"
	// SleepReasonMerged: a keep-warm worker whose PR merged, parked in place with
	// the "Merged - open to continue" affordance. Opening it resumes it, which is
	// what that affordance promises.
	SleepReasonMerged SleepReason = "merged"
)

// AsleepForTurn reports whether this session is asleep because it is not its
// turn. It is the ONE question the two facts had to be separated to answer.
func (r SessionRecord) AsleepForTurn() bool {
	return r.IsSuspended && r.SleepReason == SleepReasonTurn
}

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
	// WokenByWake: somebody asked for it - the baton bar's Wake button,
	// `ao crew wake`, or a handover driven by either.
	WokenByWake WokenBy = "wake"
	// WokenByRestore: the user restored/restarted the session.
	WokenByRestore WokenBy = "restore"
	// WokenByBoot: the daemon's boot restore pass brought it back.
	WokenByBoot WokenBy = "boot"
	// WokenBySpawn: a first launch. Never recorded on the row (a spawning session
	// was not asleep); it exists so every call site names itself.
	WokenBySpawn WokenBy = "spawn"
)
