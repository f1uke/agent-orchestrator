package ports

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// SessionEnding is everything AO knows about ONE session stopping, at the
// instant it stopped.
//
// It exists because domain.Termination - two words on the session row - could
// not answer "what ended this session" for three sessions that died 105ms apart
// on 2026-09-22: every one of them read `source=agent, reason=other`, which is
// Claude Code's catch-all, and nothing else survived. The facts that WOULD have
// narrowed it were all in AO's hands at that moment and were simply never
// written down.
//
// It is deliberately NOT a widening of the session row. A row is read on every
// board refresh; this is read when somebody is investigating, which is roughly
// never. So it travels to a sink that writes it to a capped journal off to the
// side, and the common path pays nothing.
//
// Lifecycle fills it from the record as it stood BEFORE the terminal write, so
// LastState and LastActivityAt describe the session that ended rather than the
// "exited" it is about to become.
type SessionEnding struct {
	// At is when the ending was recorded, to whatever precision the clock gives.
	// Sub-second precision is load-bearing here: 105 milliseconds is the whole
	// difference between three sessions ending and one event ending three.
	At time.Time

	SessionID domain.SessionID
	ProjectID domain.ProjectID
	Kind      domain.SessionKind
	CrewRole  domain.CrewRole
	// Harness is whose end-of-session behaviour this is. A cluster confined to
	// one harness says something a cluster spanning two does not.
	Harness domain.AgentHarness

	// Source and Reason are the same two facts the row carries, repeated here so
	// one line is self-contained and a journal can be read without the database.
	Source domain.TerminationSource
	Reason string

	// LastState is what the session was doing immediately before it stopped.
	LastState domain.ActivityState
	// LastActivityAt is when it last said anything. The gap between this and At
	// is the discriminator the row cannot give: an idle timeout ends a session
	// that has been quiet for hours, a signal ends one mid-turn.
	LastActivityAt time.Time

	// TranscriptPath is where to read what the agent was doing. Empty when the
	// harness keeps none AO can locate.
	TranscriptPath string
	// AgentSessionID is the harness's own id for the conversation that ended, so
	// the transcript can be found even if the path above has gone stale.
	AgentSessionID string

	// RuntimeHandleID names the session's terminal pane. The sink probes it, so
	// the record says whether the pane outlived the agent - which separates
	// "something killed the panes" from "something killed the processes inside
	// living panes", and cannot be recovered by looking hours later.
	RuntimeHandleID string
	WorkspacePath   string
}

// SessionEndingSink receives one SessionEnding per termination.
//
// It returns nothing. An ending is a fact that has already happened by the time
// this is called, and no failure to record it may change what AO does about it -
// the same bargain hooks.log makes.
type SessionEndingSink interface {
	RecordEnding(ctx context.Context, e SessionEnding)
}
