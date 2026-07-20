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
	// default store — nil is simply OFF.
	AutoResolveOnReply *bool `json:"autoResolveOnReply"`
	// IsTodo marks a session PREPARED BUT NOT STARTED: the board's TODO lane.
	// No branch/worktree/tmux exists yet — only the spec below is persisted.
	// Start materializes the row in place (clearing this flag in MarkSpawned),
	// so the id carries through into the live session. Durable fact; drives the
	// StatusTodo display status.
	IsTodo bool `json:"isTodo,omitempty"`
	// IsSuspended marks a session whose tmux runtime the idle sweep tore down to
	// free machine resources while KEEPING it on the board in its current lane
	// (worktree kept on disk). It is deliberately orthogonal to IsTerminated:
	// status derivation never reads it, so the card stays in its real lane and
	// the flag only drives a "paused — click to resume" affordance. Opening the
	// session resumes it in place (recreate tmux, clear this flag). Durable fact,
	// surfaced in the API read model for the paused affordance + countdown.
	IsSuspended bool `json:"isSuspended,omitempty"`
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
	// (the /wake signal). It feeds ONLY the idle-suspend keepalive — idleReference
	// takes the later of Activity.LastActivityAt and this — so viewing a session
	// refreshes its 72h idle-suspend TTL WITHOUT bumping Activity.LastActivityAt,
	// which status derivation ages needs_input/working off. Decoupling the two is
	// what keeps a mere open from flipping a "Needs you" session back to working
	// with a restarted countdown. Zero = never opened. Internal durable fact, not
	// in the API read model — its effect rides the derived IdleCloseAt.
	LastOpenedAt time.Time `json:"-"`
	// BaseBranch is the branch the worktree is created from and PRTarget is the
	// branch this session's PR merges INTO. They are NOT synonyms: BaseBranch is
	// load-bearing (it becomes the base ref of `git worktree add`), while
	// PRTarget records where the work is headed, which a gitflow hotfix can set
	// independently. Both are resolved at spawn and persisted on EVERY session —
	// deferred or immediate — so the target branch is a durable fact rather than
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
	// TokenUsage holds the per-session token totals summed from the harness
	// transcript (claude-code only; all-zero for agents without a parseable
	// transcript). Durable measured facts; the raw + cost-weighted totals and the
	// runaway flag are DERIVED at read time (see the wire mapping), never stored.
	// json:"-" — exposed via the curated tokenUsage wire object, not raw on the
	// embedded record. Written only by the dedicated token-usage setter, so the
	// full-row update path never clobbers it.
	TokenUsage TokenUsage `json:"-"`
	// TokensUpdatedAt is when TokenUsage was last (re)parsed from the transcript.
	// Zero = never parsed (no telemetry available → no chip). Internal durable fact.
	TokensUpdatedAt time.Time       `json:"-"`
	Metadata        SessionMetadata `json:"-"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

// Session is the read-model returned across the API boundary: a SessionRecord
// plus the derived display Status.
type Session struct {
	SessionRecord
	Status SessionStatus `json:"status" enum:"todo,working,pr_open,draft,ci_failed,review_pending,changes_requested,approved,mergeable,merged,needs_input,idle,terminated,no_signal"`
	// StatusReason names the derivation rule that produced Status, so the UI can
	// explain WHY (e.g. a needs_input from a lost-hook timeout vs a real agent
	// prompt). Derived on read, never stored.
	StatusReason StatusReason `json:"statusReason,omitempty" enum:"working,waiting_input,active_stale,idle_aged,idle,no_signal,pr_pipeline,terminated,merged"`
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
	// TargetBranch is the branch this session's work merges into, and
	// TargetSource names WHERE that answer came from so the UI can distinguish a
	// value the human set from one inherited off the project. Both are derived on
	// read from durable facts (the session's stored PRTarget, its PRs, the
	// project default) — never stored, so they cannot go stale against them.
	// TargetBranch is empty when nothing is known; the UI must say so rather than
	// assume "main".
	TargetBranch string `json:"targetBranch,omitempty"`
	TargetSource string `json:"targetSource,omitempty" enum:"pr,session_pr_target,session_base,project"`
	// PRs are the session's attributed pull requests (one session can own many).
	// They feed status derivation and are surfaced on the API read model. Not
	// serialized here: the HTTP boundary maps them to the curated wire shape.
	PRs []PRFacts `json:"-"`
}
