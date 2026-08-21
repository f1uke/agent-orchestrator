package domain

// SessionStatus is the single-word DISPLAY status the dashboard renders. It is
// derived from persisted session facts plus PR facts and is never stored.
type SessionStatus string

// The display statuses the dashboard renders.
const (
	// StatusTodo marks a session PREPARED BUT NOT STARTED (the board's TODO
	// lane): no branch/worktree/tmux exists yet, only the persisted spec. Start
	// materializes it and it takes on the normal derived statuses below.
	StatusTodo             SessionStatus = "todo"
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

// StatusReason values name each rule in the status derivation; the trailing
// comment on each states the condition that selects it.
const (
	ReasonTodo         StatusReason = "todo"          // prepared but not started (TODO lane)
	ReasonWorking      StatusReason = "working"       // active, heartbeat fresh
	ReasonWaitingInput StatusReason = "waiting_input" // agent reported a prompt (Notification hook)
	ReasonActiveStale  StatusReason = "active_stale"  // active aged past grace -> needs_input (timeout guess)
	ReasonIdleAged     StatusReason = "idle_aged"     // idle aged past grace -> needs_input (timeout guess)
	ReasonIdle         StatusReason = "idle"          // fresh idle within grace, or hook-less quiet
	ReasonNoSignal     StatusReason = "no_signal"     // hook-capable but never signalled
	ReasonPRPipeline   StatusReason = "pr_pipeline"   // status came from the open-PR aggregate
	ReasonTerminated   StatusReason = "terminated"    // session terminated
	ReasonMerged       StatusReason = "merged"        // merged branch / terminated with a merged PR
	// ReasonRunsDiscarded is CappedRepeat runs in a row thrown away because the
	// tree moved under each of them. The member cannot get a quiet window, and
	// the automatic retry is spent, so a human decides: pause the other member,
	// or accept an uncertified result. It outranks the PR pipeline deliberately -
	// a card must not read "mergeable" while nothing it says has been verified.
	ReasonRunsDiscarded StatusReason = "runs_discarded"
	// ReasonCrewTalkCapped is a conversation between the two agents on one task
	// that hit its cap: the last thing this member tried to say to its crewmate
	// was REFUSED, either because they have gone round CappedRepeat times on one
	// subject with nothing moving, or because the crew has spent its hourly
	// budget. The refusal is what stops the loop; this is what makes it visible
	// rather than leaving two agents mysteriously quiet. It clears itself the
	// moment a later message goes through, which happens as soon as they move on
	// to a new commit or a new case.
	ReasonCrewTalkCapped StatusReason = "crew_talk_capped"
)
