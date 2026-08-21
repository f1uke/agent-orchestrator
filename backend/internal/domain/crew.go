package domain

// A CREW is the one or two long-lived sessions that belong to ONE task and share
// ONE worktree. The design (design-multi-agent-per-task-ux) gives a `standard` or
// `deep` task a dev and a qa member; a `mechanical` task gets dev alone. Wiring
// that decision to --task-size is deliberately NOT part of the slice that
// introduced this type: the capability exists, switched off, and every spawn is
// still solo.
//
// The two members already share a worktree without any help: a worker's worktree
// directory is derived from its BRANCH, not its session id, so two sessions on one
// branch resolve to one directory. What a crew adds is the RELATIONSHIP and the
// roles - which member owns the PR, and therefore whose teardown ends the task.

// CrewRole names what one member of a crew is for.
type CrewRole string

// Crew roles. The zero value is the empty string, which means SOLO: not part of
// any crew. That is what every session a normal spawn creates carries, so every
// lifetime path that reads a role sees a no-op for ordinary traffic.
const (
	// CrewRoleDev owns the PR, the branch and the worktree. Every PR-driven nudge
	// already goes to it, and its teardown is the TASK's teardown: no member
	// outlives its dev.
	CrewRoleDev CrewRole = "dev"
	// CrewRoleQA writes, runs and records the tests in dev's worktree. It is a
	// subordinate: terminating it is local and leaves dev's workspace intact.
	CrewRoleQA CrewRole = "qa"
)

// Valid reports whether r is a known, explicitly-set role. The empty string is
// NOT valid - it is the solo marker, not a role - so Valid is what rejects a
// garbage value at a boundary.
func (r CrewRole) Valid() bool {
	switch r {
	case CrewRoleDev, CrewRoleQA:
		return true
	}
	return false
}

// IsDev reports whether this member owns the crew's worktree and PR.
func (r CrewRole) IsDev() bool { return r == CrewRoleDev }

// InCrew reports whether this session belongs to a crew at all. A solo session
// carries neither field, and every lifetime decision that branches on crew
// membership asks this first, so solo behaviour is decided by a zero value rather
// than by remembering to special-case it.
func (r SessionRecord) InCrew() bool {
	return r.CrewID != "" && r.CrewRole.Valid()
}

// OwnsCrewWorkspace reports whether this session is the member that may create,
// capture and destroy the shared worktree. A solo session owns its own workspace
// (there is nobody to share it with); inside a crew only dev does.
func (r SessionRecord) OwnsCrewWorkspace() bool {
	return !r.InCrew() || r.CrewRole.IsDev()
}

// Awake reports whether AO believes this session HAS A RUNNING AGENT right now.
//
// It is the definition ONE-AWAKE-AT-A-TIME is enforced on, so it is worth saying
// exactly why it is these three fields and not the activity state:
//
//   - Terminated: the session is over and its runtime was destroyed.
//   - Suspended: the tmux was reaped and the worktree kept. There is NO process.
//     Only MarkSpawned (spawn, restore, resume, restart) clears the flag, so AO
//     is the sole author of the transition in both directions.
//   - Todo: prepared but never started - a row with no branch, no worktree and no
//     runtime.
//
// Activity state (`active` / `parked`) is deliberately NOT part of this. It is a
// READING reported by the agent's own CLI hook (see activity.go), not something
// AO writes, and a parked agent still owns a live pane: a human, a nudge or a
// queued message can put it back to work at any instant. An exclusion built on it
// would be a convention the agents are asked to respect. Awake is a fact AO
// controls, which is what makes refusing possible at all.
func (r SessionRecord) Awake() bool {
	return !r.IsTerminated && !r.IsSuspended && !r.IsTodo
}

// CrewJoinReason says WHAT CREATED a crew member, and it is the one durable fact
// lazy creation adds.
//
// A qa is no longer formed at spawn: a task starts as dev alone and gains a qa
// the first time dev touches a RUNTIME SURFACE - claiming the simulator, or
// pointing `ao preview` at the app (design §1.12.1). A backend-only task never
// does either, so it never gets a qa and never pays for one.
//
// There is exactly ONE transition, absent -> present, one way and once, so this
// enum is all the audit trail the event needs: everything else the board says
// about the join ("when") is already on the member's CreatedAt. The board turns
// it into one sentence - `qa joined · dev opened the simulator` - which is what
// makes a card that moves BACKWARD (from ready-to-merge to in-review, as the
// smoke gate gains a real input) legible instead of surprising.
//
// The zero value is "not recorded": every qa created before this existed, which
// is why the board falls back to saying nothing rather than guessing.
type CrewJoinReason string

// The three ways a member joins a task.
const (
	// CrewJoinSim: dev took the simulator lease. `ao sim claim`, or any gesture,
	// which cannot touch a device without one.
	CrewJoinSim CrewJoinReason = "sim"
	// CrewJoinPreview: dev pointed `ao preview` at the app, moving the session's
	// preview_url / preview_revision.
	CrewJoinPreview CrewJoinReason = "preview"
	// CrewJoinManual: a human asked for it - `ao crew add`, or the card's `+ qa`.
	CrewJoinManual CrewJoinReason = "manual"
)

// Valid reports whether r is a recorded reason. The empty string is NOT valid:
// it is "we do not know", which is what every row written before lazy creation
// carries.
func (r CrewJoinReason) Valid() bool {
	switch r {
	case CrewJoinSim, CrewJoinPreview, CrewJoinManual:
		return true
	}
	return false
}

// Automatic reports whether AO created this member by OBSERVING dev, rather than
// because a person asked for it.
func (r CrewJoinReason) Automatic() bool {
	return r == CrewJoinSim || r == CrewJoinPreview
}
