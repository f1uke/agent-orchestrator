package ports

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ProbeResult is a single liveness reading. "failed" means the probe errored
// or timed out and is never treated as a death conclusion.
type ProbeResult string

// Probe readings. Alive/Dead are conclusions; Failed is ignored by lifecycle
// because it is not a reliable death decision.
const (
	ProbeAlive  ProbeResult = "alive"
	ProbeDead   ProbeResult = "dead"
	ProbeFailed ProbeResult = "failed"
)

// RuntimeFacts is what the reaper reports each probe of a session runtime.
type RuntimeFacts struct {
	ObservedAt time.Time
	Probe      ProbeResult
}

// SessionEnd is what an end-of-session hook reports about the ending itself. It
// is a CURATED shape: the hook process whitelists a short reason token out of
// the harness's native payload, so nothing else from that payload — paths, tool
// bodies, transcripts — reaches the daemon.
type SessionEnd struct {
	// Reason is the harness's own end reason, e.g. Claude Code's
	// "prompt_input_exit" or "other". Empty when the harness reports none, which
	// the reducer records as domain.TerminationReasonUnknown.
	Reason string
}

// ActivitySignal is pushed by the agent hooks. Only a Valid signal is
// authoritative; a stale/absent one is ignored rather than read as idleness.
type ActivitySignal struct {
	Valid     bool
	State     domain.ActivityState
	Timestamp time.Time
	// End carries what the harness said about an ENDING signal, so a session
	// that stops on its own can account for itself. Nil for every other state,
	// and nil-tolerated on an exit: a harness that reports no reason still
	// records that the agent, not AO, ended the session.
	End *SessionEnd
}
