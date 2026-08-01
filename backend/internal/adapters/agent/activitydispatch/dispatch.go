// Package activitydispatch is the single source of truth mapping the agent
// token in `ao hooks <agent> <event>` onto the function that interprets that
// agent's hook callbacks as an AO activity state.
//
// The hidden `ao hooks` CLI command dispatches a live callback through it. Every
// adapter that installs `ao hooks <tok>` callbacks must have a deriver
// registered here — otherwise the adapter writes callbacks that nothing on the
// receiving side understands, so its activity is silently never reported.
package activitydispatch

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/activitystate"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/droid"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// DeriveFunc maps a native agent hook event and its raw stdin payload onto an AO
// activity state. ok=false means the event carries no activity signal.
type DeriveFunc func(event string, payload []byte) (domain.ActivityState, bool)

// Derivers maps the agent token in `ao hooks <agent> <event>` to its deriver.
// Per-adapter PRs add their tokens here as they land.
var Derivers = map[string]DeriveFunc{
	// Adapters that parse hook payloads for finer-grained state keep their own
	// deriver; the rest share the name-only StandardDeriveActivityState.
	"claude-code": claudecode.DeriveActivityState,
	"codex":       codex.DeriveActivityState,
	"droid":       droid.DeriveActivityState,
	"agy":         agy.DeriveActivityState,
	"opencode":    opencode.DeriveActivityState,
	"goose":       activitystate.StandardDeriveActivityState,
	"cursor":      activitystate.StandardDeriveActivityState,
	"qwen":        activitystate.StandardDeriveActivityState,
	"copilot":     activitystate.StandardDeriveActivityState,
	"cline":       activitystate.StandardDeriveActivityState,
	"kiro":        activitystate.StandardDeriveActivityState,
	"kilocode":    activitystate.StandardDeriveActivityState,
	"autohand":    activitystate.StandardDeriveActivityState,
}

// Derive looks up the deriver for an agent token and applies it. ok=false when
// the token has no registered deriver or the event carries no activity signal —
// the caller reports nothing in either case.
func Derive(agent, event string, payload []byte) (domain.ActivityState, bool) {
	derive, found := Derivers[agent]
	if !found {
		return "", false
	}
	return derive(event, payload)
}

// DetailFunc maps a native agent hook event and its raw stdin payload onto the
// CURATED detail of what the agent is doing. ok=false means the event carries no
// per-action detail.
//
// The payload is raw and unfiltered (a Write's input is a whole file, a Bash
// command can carry a token), so every implementation must funnel it through
// adapters/agent/toolcurate rather than reading fields out of it directly. This
// runs inside `ao hooks`, so the raw payload never crosses a process boundary.
type DetailFunc func(event string, payload []byte) (domain.ActivityDetail, bool)

// DetailDerivers maps the agent token in `ao hooks <agent> <event>` to its
// curated-detail deriver. It is deliberately sparser than Derivers: only
// harnesses whose hooks actually report per-tool activity appear, and a harness
// missing here simply reports activity states with no detail. That degradation
// is silent by design — a bubble says less on those harnesses rather than
// badging the absence.
var DetailDerivers = map[string]DetailFunc{
	"claude-code": claudecode.DeriveActivityDetail,
	"opencode":    opencode.DeriveActivityDetail,
	"agy":         agy.DeriveActivityDetail,
}

// DeriveDetail looks up the detail deriver for an agent token and applies it.
// ok=false when the token has no detail deriver or the event carries no detail.
func DeriveDetail(agent, event string, payload []byte) (domain.ActivityDetail, bool) {
	derive, found := DetailDerivers[agent]
	if !found {
		return domain.ActivityDetail{}, false
	}
	return derive(event, payload)
}

// DenyFunc reports whether a native agent hook callback must be REFUSED, and
// the reason handed back to the agent. Unlike the derivers above it can change
// what the agent does, so it is only ever consulted for callbacks that run
// before the action they describe.
type DenyFunc func(event string, payload []byte) (bool, string)

// NestedWorktreeDeniers maps the agent token in `ao hooks <agent> <event>` to
// the check that refuses a nested worktree for a same-task child. An AO worker
// already owns an isolated worktree on its branch, so a child that creates or
// enters another one takes its edits off the worker's branch.
//
// It is sparser than Derivers by design: only harnesses that can both spawn
// child agents into their own checkout AND let a hook veto that appear. A
// harness missing here is simply never denied.
var NestedWorktreeDeniers = map[string]DenyFunc{
	"claude-code": claudecode.NestedWorktreeDenial,
}

// DenyNestedWorktree looks up the denier for an agent token and applies it.
// deny=false when the token has no denier or the callback is harmless.
func DenyNestedWorktree(agent, event string, payload []byte) (bool, string) {
	deny, found := NestedWorktreeDeniers[agent]
	if !found {
		return false, ""
	}
	return deny(event, payload)
}

// SupportsHarness reports whether a harness has an activity pipeline at all:
// a registered deriver here means its adapter installs `ao hooks <harness>`
// callbacks that can reach the daemon. Status derivation uses this to decide
// whether prolonged silence is suspicious (no_signal) or simply all a hook-less
// harness can ever report (idle). Harness names and `ao hooks` agent tokens are
// the same strings by convention.
func SupportsHarness(h domain.AgentHarness) bool {
	_, ok := Derivers[string(h)]
	return ok
}
