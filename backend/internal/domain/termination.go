package domain

import "time"

// TerminationSource names WHO ended a session. It is the first question asked of
// a session that stopped without explaining itself, and the three answers are
// genuinely different situations: AO decided, the agent decided, or nobody said
// and AO inferred it from a runtime that was no longer there.
type TerminationSource string

const (
	// TerminationSourceAgent means the agent's own end-of-session hook reported
	// the exit. AO did not ask for it; the harness stopped on its own terms.
	TerminationSourceAgent TerminationSource = "agent"
	// TerminationSourceAO means AO tore the session down. Reason carries the
	// named cause below, so a teardown can be attributed to the operation that
	// ordered it rather than to "something".
	TerminationSourceAO TerminationSource = "ao"
	// TerminationSourceRuntimeGone means nobody reported anything and the reaper
	// found the runtime missing. This is the only inferred answer, and naming it
	// as inference keeps it from being read as a report.
	TerminationSourceRuntimeGone TerminationSource = "runtime_gone"
)

// TerminationReasonUnknown is the reason recorded when the ending is real but
// unexplained — an agent whose harness reports no reason, or one AO does not
// recognise. It is deliberately a value rather than an empty string: "the agent
// exited and did not say why" is an answer, and it must not be mistaken for a
// record that was never written.
const TerminationReasonUnknown = "unknown"

// AO's own termination causes. Each names the operation that ordered the
// teardown, so the record answers "who did this to my session" without
// cross-reading the daemon log, the reclaim log and the change log.
const (
	// TerminationCauseKill is an explicit kill/close of one session.
	TerminationCauseKill = "kill"
	// TerminationCauseAutoReclaim is the background disk-reclaim loop.
	TerminationCauseAutoReclaim = "auto_reclaim"
	// TerminationCausePurge is a delete: teardown plus removal of the row.
	TerminationCausePurge = "purge"
	// TerminationCauseDaemonShutdown is the save-and-teardown sweep the daemon
	// runs on shutdown, which terminates every live session on purpose.
	TerminationCauseDaemonShutdown = "daemon_shutdown"
	// TerminationCauseSpawnRollback is a spawn that failed part-way and undid
	// itself, so the session never really started.
	TerminationCauseSpawnRollback = "spawn_rollback"
	// TerminationCauseReplaced is an orchestrator retired so a replacement can
	// claim its canonical branch.
	TerminationCauseReplaced = "replaced"
	// TerminationCauseWorkComplete is the session finishing its work — every PR
	// it owns is merged and none is left open.
	TerminationCauseWorkComplete = "work_complete"
	// TerminationCauseRuntimeMissing is the startup reconcile finding a session
	// whose runtime did not survive however the previous daemon died, and the
	// reaper finding a runtime that is simply no longer there.
	TerminationCauseRuntimeMissing = "runtime_missing"
	// TerminationCauseRestart is the terminal beat of a restart-in-place, between
	// destroying the old runtime and relaunching into the same worktree.
	TerminationCauseRestart = "restart"
	// TerminationCauseDevExited is a crew member ended because its DEV's agent
	// ended its own session. dev owns the branch, the worktree and the PR, so a
	// subordinate left running after it would be working on something nobody
	// will land.
	TerminationCauseDevExited = "dev_exited"
	// TerminationCauseIssueClosed is the tracker issue behind the session
	// reaching a terminal state.
	TerminationCauseIssueClosed = "issue_closed"
	// TerminationCauseDiscardWork is a kill somebody ordered KNOWING it would
	// destroy uncommitted work, having been shown the file list first. It is
	// distinct from a plain kill because the two are different events to find
	// afterwards: one ended a session, the other ended a session and threw work
	// away (recoverably - see refs/ao/preserved/<session-id>).
	TerminationCauseDiscardWork = "discard_work"
)

// Termination is the account a session leaves of how it reached its terminal
// state. It exists because "activity=exited" alone is unaccountable: a worker
// that stopped by itself mid-task and one AO reclaimed look identical on the
// row, and the difference is exactly what someone asking "why did it disappear?"
// needs.
//
// Every field is a fact AO actually holds at termination time. Nothing here is
// inferred after the fact, and nothing is invented when the answer is not known
// (see TerminationReasonUnknown). AO has no exit code or signal to record: an
// agent runs inside a terminal pane AO does not wait() on, so the harness's own
// reason is the closest thing to one and is what Reason carries.
type Termination struct {
	// Source is who ended the session.
	Source TerminationSource
	// Reason is the harness's own end reason when Source is the agent, or the
	// named AO cause when AO ordered it. Never empty on a recorded termination.
	Reason string
	// LastState is the activity state the session was in immediately before it
	// ended — "it was still working" versus "it had been waiting on me".
	LastState ActivityState
	// TranscriptPath points at the agent transcript as it was at termination.
	// Snapshotted rather than derived on read because the worktree it is derived
	// from may be reclaimed later. Empty when the harness keeps no transcript AO
	// can locate.
	TranscriptPath string
	// At is when the termination was recorded.
	At time.Time
}

// IsZero reports whether no termination has been recorded — a live session, or
// one terminated before AO kept this account.
func (t Termination) IsZero() bool { return t == Termination{} }
