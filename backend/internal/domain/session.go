package domain

import "time"

// These ID types are distinct string types so they can't be swapped at a call
// site by accident.
type (
	// SessionID identifies a session.
	SessionID string
	// ProjectID identifies a project.
	ProjectID string
	// IssueID identifies a tracker issue.
	IssueID string
)

// SessionKind distinguishes a worker session from an orchestrator session.
type SessionKind string

// Session kinds.
const (
	KindWorker       SessionKind = "worker"
	KindOrchestrator SessionKind = "orchestrator"
)

// SessionMetadata is the typed, off-status metadata for a session: operational
// handles and seed inputs used by Session Manager and reaper.
type SessionMetadata struct {
	Branch          string `json:"branch,omitempty"`
	WorkspacePath   string `json:"workspacePath,omitempty"`
	RuntimeHandleID string `json:"runtimeHandleId,omitempty"`
	AgentSessionID  string `json:"agentSessionId,omitempty"`
	Prompt          string `json:"prompt,omitempty"`
	// PreviewURL is the browser preview target the desktop app opens for this
	// session. Set via `ao preview` (POST /sessions/{id}/preview); persisted so
	// it survives a daemon restart. Empty means no preview has been requested.
	PreviewURL string `json:"previewUrl,omitempty"`
	// PreviewRevision is a monotonic counter bumped on every `ao preview` call,
	// even when PreviewURL is unchanged. The desktop browser panel keys
	// navigation on it so a repeated `ao preview <same-url>` still refreshes.
	PreviewRevision int64 `json:"previewRevision,omitempty"`
}

// SessionRecord is the persistence shape. It intentionally stores only durable
// facts: identity, agent harness, activity_state, is_terminated, and operational
// metadata. The user-facing Status is derived from these facts plus PR facts.
type SessionRecord struct {
	ID          SessionID    `json:"id"`
	ProjectID   ProjectID    `json:"projectId"`
	IssueID     IssueID      `json:"issueId,omitempty"`
	Kind        SessionKind  `json:"kind"`
	Harness     AgentHarness `json:"harness,omitempty"`
	DisplayName string       `json:"displayName,omitempty"`
	Activity    Activity     `json:"activity"`
	// FirstSignalAt is when the FIRST agent hook callback arrived for the
	// current spawn/restore: raw signal receipt, independent of the derived
	// activity state. Zero means no hook has ever reported, which deriveStatus
	// surfaces as StatusNoSignal after a grace period. Internal fact, not part
	// of the API read model.
	FirstSignalAt time.Time `json:"-"`
	IsTerminated  bool      `json:"isTerminated"`
	// Reactivated marks a session brought back from a terminal state by
	// `ao session restore` (the board Reopen action). It stays set while the
	// session is live so status derivation surfaces a reopened session as
	// needs_input (the "Needs you" zone) instead of letting a previously-merged PR
	// pin it to Done, until it takes on new work or is finished again. Internal
	// durable fact, not part of the API read model.
	Reactivated bool `json:"-"`
	// AutoNudgeComments overrides, per session, whether the worker is
	// auto-nudged when its PR has unresolved review comments. nil = inherit the
	// global default (autonudge settings); non-nil = explicit on/off. Exposed in
	// the API read model so the Comments-tab switch can show/set it.
	AutoNudgeComments *bool `json:"autoNudgeComments"`
	// AutoResolveOnReply gates, per session, whether the SCM observer auto-resolves
	// a review thread once OUR side (the PR author / token user) posts a new reply
	// on it while it is still unresolved. nil/false = OFF (the default: resolving is
	// left to the reviewer); true = ON. Exposed in the API read model so the
	// Reviews-tab switch can show/set it. Unlike AutoNudgeComments there is no global
	// default store - nil is simply OFF.
	AutoResolveOnReply *bool `json:"autoResolveOnReply"`
	// IsTodo marks a session PREPARED BUT NOT STARTED: the board's TODO lane.
	// No branch/worktree/tmux exists yet - only the spec below is persisted.
	// Start materializes the row in place (clearing this flag in MarkSpawned),
	// so the id carries through into the live session. Durable fact; drives the
	// StatusTodo display status.
	IsTodo bool `json:"isTodo,omitempty"`
	// IsSuspended marks a session whose tmux runtime the idle sweep tore down to
	// free machine resources while KEEPING it on the board in its current lane
	// (worktree kept on disk). It is deliberately orthogonal to IsTerminated:
	// status derivation never reads it, so the card stays in its real lane and
	// the flag only drives a "paused - click to resume" affordance. Opening the
	// session resumes it in place (recreate tmux, clear this flag). Durable fact,
	// surfaced in the API read model for the paused affordance + countdown.
	IsSuspended bool `json:"isSuspended,omitempty"`
	// SleepReason says WHY IsSuspended is set. Every reader of IsSuspended except
	// the card's copy - the message queue, the reaper, boot reconciliation, the
	// shutdown sweep, status derivation - wants the same answer whatever the
	// reason ("there is no process here"), which is why this is an ANNOTATION on
	// the existing state rather than a second state.
	//
	// Empty means "not recorded" - every row written before this field existed,
	// and any path that forgets. It behaves exactly as an unannotated suspend
	// always did, which since the crew's turn-taking was removed is what EVERY
	// reason does: opening a suspended session resumes it. Written when
	// IsSuspended is set and cleared when the runtime comes back, so at most one
	// of this and WokenBy is set at a time.
	SleepReason SleepReason `json:"sleepReason,omitempty" enum:"idle,turn,merged,undelivered"`
	// WokenBy records WHAT brought this session's runtime back after a suspend.
	// change_log fans out the is_suspended flip but not the actor, so a wake that
	// nobody remembers ordering (the incident this field was added for) could only
	// be reasoned about from timestamps. Written by MarkSpawned when - and only
	// when - the row it is reviving was suspended, so a fresh spawn leaves it
	// empty, and cleared again on the next suspend. Internal durable fact: it
	// rides the session_updated CDC payload, which is where the question gets
	// asked.
	WokenBy WokenBy `json:"-"`
	// KeepWarmOnMerge marks a WORKER expected to open MORE PRs after the current
	// one merges (an orchestrator-dispatched multi-slice worker). When true, a PR
	// merge that would finish the session SUSPENDS it in place (card stays on the
	// board, resumable) instead of terminating it to Done
	// (feature/merge-suspend-in-place). Default false: an ordinary single-PR worker
	// still auto-archives to Done on merge. Opt-in per session via
	// `ao spawn --keep-warm` or the board card toggle. Durable fact, surfaced in the
	// API read model so the toggle reflects its state.
	KeepWarmOnMerge bool `json:"keepWarmOnMerge,omitempty"`
	// LastOpenedAt is when the user last OPENED/selected this session in the UI
	// (the /wake signal). It feeds ONLY the idle-suspend keepalive - idleReference
	// takes the later of Activity.LastActivityAt and this - so viewing a session
	// refreshes its 72h idle-suspend TTL WITHOUT bumping Activity.LastActivityAt,
	// which status derivation ages needs_input/working off. Decoupling the two is
	// what keeps a mere open from flipping a "Needs you" session back to working
	// with a restarted countdown. Zero = never opened. Internal durable fact, not
	// in the API read model - its effect rides the derived IdleCloseAt.
	LastOpenedAt time.Time `json:"-"`
	// BaseBranch is the branch the worktree is created from and PRTarget is the
	// branch this session's PR merges INTO. They are NOT synonyms: BaseBranch is
	// load-bearing (it becomes the base ref of `git worktree add`), while
	// PRTarget records where the work is headed, which a gitflow hotfix can set
	// independently. Both are resolved at spawn and persisted on EVERY session -
	// deferred or immediate - so the target branch is a durable fact rather than
	// something each reader re-derives; PRTarget is additionally editable by the
	// human, which retargets a live PR/MR on the SCM to keep the two in step.
	// (Sessions created before this was recorded carry empty values and fall back
	// through resolveTargetBranch; no backfill guesses on their behalf.)
	//
	// AutoNameBranch asks for an AI branch name when Branch is empty; CreatedBy
	// is the orchestrator session that queued the task (for report-back). Those
	// two remain part of the deferred TODO spec, replayed verbatim on Start.
	BaseBranch     string    `json:"baseBranch,omitempty"`
	AutoNameBranch bool      `json:"autoNameBranch,omitempty"`
	PRTarget       string    `json:"prTarget,omitempty"`
	CreatedBy      SessionID `json:"createdBy,omitempty"`
	// TaskSize is the ceremony level captured at spawn (`ao spawn --task-size`):
	// mechanical / standard / deep. It drives only the worker system prompt (a
	// mechanical task is authorized to skip the heavyweight process skills) and is
	// persisted so a restore or a TODO Start rebuilds the prompt at the right level.
	// Empty on old rows / normal spawns; WithDefault resolves that to standard (full
	// ceremony). Internal durable fact, not part of the API read model.
	TaskSize TaskSize `json:"-"`
	// CrewID and CrewRole represent the CREW: the one or two long-lived sessions
	// that belong to ONE task and share ONE worktree (dev + qa).
	//
	// CrewID is the DEV member's session id, carried by every member including dev
	// itself, so the crew key and the id a human types into `ao send` are the same
	// string. CrewRole names what this member is FOR: dev owns the PR and the
	// branch and is where every PR-driven nudge already goes; a qa member is a
	// subordinate that shares dev's worktree and never outlives it.
	//
	// BOTH EMPTY MEANS SOLO, which is every session a normal spawn creates. Solo is
	// the zero value on purpose: the lifetime paths (teardown, reclaim, the idle
	// sweep) read these fields, and a zero value makes them a no-op, so a solo
	// task's behaviour is unchanged rather than merely intended to be.
	//
	// Durable facts, set once when the crew is formed and never toggled. Not part
	// of the API read model - nothing in the UI reads them.
	CrewID   SessionID `json:"-"`
	CrewRole CrewRole  `json:"-"`
	// CrewJoinReason is WHAT CREATED this member: dev touching the simulator, dev
	// pointing `ao preview` at the app, or a human asking for it. Empty on dev, on
	// every solo session, and on a qa created before lazy creation existed.
	// Durable, written once with the row and never toggled - there is one
	// transition and it is one-way. Not part of the API read model directly: it
	// reaches the board through the curated crew wire object, which is what turns
	// it into the join line under the crew strip.
	CrewJoinReason CrewJoinReason `json:"-"`
	// TokenUsage holds the per-session token totals summed from the harness
	// transcript (claude-code only; all-zero for agents without a parseable
	// transcript). Durable measured facts; the raw + cost-weighted totals and the
	// runaway flag are DERIVED at read time (see the wire mapping), never stored.
	// json:"-" - exposed via the curated tokenUsage wire object, not raw on the
	// embedded record. Written only by the dedicated token-usage setter, so the
	// full-row update path never clobbers it.
	TokenUsage TokenUsage `json:"-"`
	// TokensUpdatedAt is when TokenUsage was last (re)parsed from the transcript.
	// Zero = never parsed (no telemetry available → no chip). Internal durable fact.
	TokensUpdatedAt time.Time `json:"-"`
	// Termination is the account of HOW this session reached its terminal state:
	// who ended it, why, what it was doing, and where its transcript is. Written
	// by the lifecycle reducer on every terminal transition and cleared on
	// respawn. Zero for a live session and for sessions terminated before AO kept
	// this account. json:"-" - exposed via the curated termination wire object.
	Termination Termination     `json:"-"`
	Metadata    SessionMetadata `json:"-"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

// CanReceiveMessage reports whether a message may be typed at this session's
// pane right now. It is false when the pane is gone (suspended: the tmux was
// reaped, the stored handle points at nothing) and when the pane is there but
// the agent is not listening (waiting_input: a permission dialog owns the
// keyboard, so the text is eaten by the dialog and the trailing Enter can ANSWER
// it). A caller that has something to say must HOLD it in those cases, never
// drop it.
//
// Termination is deliberately not folded in: a terminated session is not
// "temporarily unable to receive", it is over, and holding a message for it
// would be a promise AO cannot keep. Callers refuse that case outright.
func (r SessionRecord) CanReceiveMessage() bool {
	return !r.IsSuspended && r.Activity.State.IsListening()
}

// NeverStarted reports whether this session has never had a runtime at all - no
// tmux, no agent, nothing spent. A crew's qa is created as a ROW (dev's branch,
// dev's worktree, its kickoff prompt) and stays that way until somebody starts
// it, and a runtime handle is written the first time one is launched and never
// cleared afterwards.
//
// It is the fact behind the ONE thing that survived the crew's turn-taking:
// LOOKING AT A CARD MUST NOT START AN AGENT. Resuming a session that was paused
// is what a glance should do and always has; starting one for the first time
// spends money and is a decision, and it was a glance doing exactly that which
// left a qa nobody had woken running twelve seconds after its dev's PR merged.
func (r SessionRecord) NeverStarted() bool {
	return r.Metadata.RuntimeHandleID == ""
}

// Session is the read-model returned across the API boundary: a SessionRecord
// plus the derived display Status.
type Session struct {
	SessionRecord
	Status SessionStatus `json:"status" enum:"todo,working,pr_open,draft,ci_failed,review_pending,changes_requested,approved,mergeable,merged,needs_input,idle,terminated,no_signal"`
	// StatusReason names the derivation rule that produced Status, so the UI can
	// explain WHY (e.g. a needs_input from a lost-hook timeout vs a real agent
	// prompt). Derived on read, never stored.
	StatusReason StatusReason `json:"statusReason,omitempty" enum:"working,waiting_input,active_stale,idle_aged,idle,no_signal,pr_pipeline,terminated,merged,runs_discarded"`
	// NextTransitionAt is when the current timeout-based reading will flip if no
	// new signal arrives; nil when the status is sticky/terminal. NextTransitionTo
	// is what it becomes. Both derived on read.
	NextTransitionAt *time.Time    `json:"nextTransitionAt,omitempty"`
	NextTransitionTo SessionStatus `json:"nextTransitionTo,omitempty" enum:"todo,working,pr_open,draft,ci_failed,review_pending,changes_requested,approved,mergeable,merged,needs_input,idle,terminated,no_signal"`
	// IdleCloseAt is when this live session will be auto-suspended by the idle
	// sweep if no further activity arrives: idleReference(rec) + the configured
	// idle TTL. Nil when the sweep is disabled (TTL 0) or the session is not a
	// live suspend candidate (terminated, a prepared TODO, or already suspended).
	// Derived on read from durable facts; drives the board/sidebar countdown.
	IdleCloseAt      *time.Time `json:"idleCloseAt,omitempty"`
	TerminalHandleID string     `json:"terminalHandleId,omitempty"`
	// QueuedMessages is how many messages AO is HOLDING for this session because
	// it could not receive them (it was suspended when they were sent); they are
	// delivered once its agent is listening again. QueuedMessagesFailed is how
	// many were given up on and will never arrive. Both are derived on read from
	// the session_message_queue table, and both exist so "waiting" and "dropped"
	// are visible to the human instead of only to the daemon log.
	QueuedMessages       int `json:"queuedMessages,omitempty"`
	QueuedMessagesFailed int `json:"queuedMessagesFailed,omitempty"`
	// TargetBranch is the branch this session's work merges into, and
	// TargetSource names WHERE that answer came from so the UI can distinguish a
	// value the human set from one inherited off the project. Both are derived on
	// read from durable facts (the session's stored PRTarget, its PRs, the
	// project default) - never stored, so they cannot go stale against them.
	// TargetBranch is empty when nothing is known; the UI must say so rather than
	// assume "main".
	TargetBranch string `json:"targetBranch,omitempty"`
	TargetSource string `json:"targetSource,omitempty" enum:"pr,session_pr_target,session_base,project"`
	// PRs are the session's attributed pull requests (one session can own many).
	// They feed status derivation and are surfaced on the API read model. Not
	// serialized here: the HTTP boundary maps them to the curated wire shape.
	PRs []PRFacts `json:"-"`
	// CrewRun is the bracketed build/test/device run this member has OPEN right
	// now, or nil when it is not running one.
	//
	// It is on the read model because "qa is running a build" cannot be derived
	// from anything else AO holds: ActivityState is reported by the agent's own
	// hooks and cannot tell a build from an agent reading a file. The bracket the
	// tree-write detector already needs is the only place that fact exists, so
	// the board reads it from here rather than from a second mechanism.
	// Not serialized here; the HTTP boundary maps it to the curated wire shape.
	CrewRun *CrewRun `json:"-"`
	// CrewRunDiscards is how many of this member's runs, ending most-recent
	// first, were thrown away because the tree moved under them - the CURRENT
	// streak, not a lifetime count. At CappedRepeat the task parks at NEEDS YOU.
	// Derived on read; one trusted run clears it.
	CrewRunDiscards int `json:"-"`
}
