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
	AutoNudgeComments *bool           `json:"autoNudgeComments"`
	Metadata          SessionMetadata `json:"-"`
	CreatedAt         time.Time       `json:"createdAt"`
	UpdatedAt         time.Time       `json:"updatedAt"`
}

// Session is the read-model returned across the API boundary: a SessionRecord
// plus the derived display Status.
type Session struct {
	SessionRecord
	Status SessionStatus `json:"status" enum:"working,pr_open,draft,ci_failed,review_pending,changes_requested,approved,mergeable,merged,needs_input,idle,terminated,no_signal"`
	// StatusReason names the derivation rule that produced Status, so the UI can
	// explain WHY (e.g. a needs_input from a lost-hook timeout vs a real agent
	// prompt). Derived on read, never stored.
	StatusReason StatusReason `json:"statusReason,omitempty" enum:"working,waiting_input,active_stale,idle_aged,idle,no_signal,pr_pipeline,terminated,merged"`
	// NextTransitionAt is when the current timeout-based reading will flip if no
	// new signal arrives; nil when the status is sticky/terminal. NextTransitionTo
	// is what it becomes. Both derived on read.
	NextTransitionAt *time.Time    `json:"nextTransitionAt,omitempty"`
	NextTransitionTo SessionStatus `json:"nextTransitionTo,omitempty" enum:"working,pr_open,draft,ci_failed,review_pending,changes_requested,approved,mergeable,merged,needs_input,idle,terminated,no_signal"`
	TerminalHandleID string        `json:"terminalHandleId,omitempty"`
	// PRs are the session's attributed pull requests (one session can own many).
	// They feed status derivation and are surfaced on the API read model. Not
	// serialized here: the HTTP boundary maps them to the curated wire shape.
	PRs []PRFacts `json:"-"`
}
