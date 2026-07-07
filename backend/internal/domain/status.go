package domain

// SessionStatus is the single-word DISPLAY status the dashboard renders. It is
// derived from persisted session facts plus PR facts and is never stored.
type SessionStatus string

// The display statuses the dashboard renders.
const (
	StatusWorking          SessionStatus = "working"
	StatusPROpen           SessionStatus = "pr_open"
	StatusDraft            SessionStatus = "draft"
	StatusCIFailed         SessionStatus = "ci_failed"
	StatusReviewPending    SessionStatus = "review_pending"
	StatusChangesRequested SessionStatus = "changes_requested"
	StatusApproved         SessionStatus = "approved"
	StatusMergeable        SessionStatus = "mergeable"
	StatusMerged           SessionStatus = "merged"
	StatusNeedsInput       SessionStatus = "needs_input"
	StatusIdle             SessionStatus = "idle"
	StatusTerminated       SessionStatus = "terminated"
	// StatusNoSignal marks a live session whose agent has never delivered a
	// hook callback for the current spawn/restore: AO cannot tell whether the
	// agent is working or stuck (broken hook pipeline, blocked interactive
	// prompt). Rendered instead of a confident idle.
	StatusNoSignal SessionStatus = "no_signal"
)

// StatusReason names which rule in the status derivation produced the display
// Status, so the UI can explain WHY a session reads working/needs_input/etc.
// It is derived on read alongside Status and never stored. A needs_input from a
// timeout guess (ReasonActiveStale/ReasonIdleAged) is thereby distinguishable
// from one the agent actually asked for (ReasonWaitingInput).
type StatusReason string

const (
	ReasonWorking      StatusReason = "working"       // active, heartbeat fresh
	ReasonWaitingInput StatusReason = "waiting_input" // agent reported a prompt (Notification hook)
	ReasonActiveStale  StatusReason = "active_stale"  // active aged past grace -> needs_input (timeout guess)
	ReasonIdleAged     StatusReason = "idle_aged"     // idle aged past grace -> needs_input (timeout guess)
	ReasonIdle         StatusReason = "idle"          // fresh idle within grace, or hook-less quiet
	ReasonNoSignal     StatusReason = "no_signal"     // hook-capable but never signalled
	ReasonPRPipeline   StatusReason = "pr_pipeline"   // status came from the open-PR aggregate
	ReasonTerminated   StatusReason = "terminated"    // session terminated
	ReasonMerged       StatusReason = "merged"        // merged branch / terminated with a merged PR
)
