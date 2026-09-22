package daemon

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	"github.com/aoagents/agent-orchestrator/backend/internal/inputgate"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// gatedRuntime wraps the selected runtime so that message injection (SendMessage)
// waits for a typing gap before landing in the pane. Every other runtime method
// is forwarded unchanged via the embedded interface, so attach/liveness/spawn are
// untouched. This is the single choke point for message delivery: it gates BOTH
// `ao send` / lifecycle nudges (through the messenger) and review nudges (through
// the review launcher), which each call SendMessage on the shared runtime.
type gatedRuntime struct {
	runtimeselect.Runtime
	gate *inputgate.Gate
}

// newGatedRuntime wraps inner so SendMessage defers to the gate. gate may be nil,
// in which case SendMessage is a plain pass-through (WaitForQuiet no-ops on nil).
//
// The wrapper must not hide what inner can do. Its method set is the union
// interface it embeds, and AgentAlive is an OPTIONAL capability outside that
// interface (conpty cannot implement it), so a bare gatedRuntime answers "no" to
// every `runtime.(ports.AgentLivenessProber)` - and every consumer handed it
// quietly fell back to pane existence. That made the session manager's
// reap-safety check a no-op and let Resume adopt a pane whose agent had exited,
// which is every pane an agent leaves behind (the keep-alive shell outlives it).
// So when inner has the capability, the wrapper carries it through.
func newGatedRuntime(inner runtimeselect.Runtime, gate *inputgate.Gate) runtimeselect.Runtime {
	g := gatedRuntime{Runtime: inner, gate: gate}
	if prober, ok := inner.(ports.AgentLivenessProber); ok {
		return gatedProbingRuntime{gatedRuntime: g, prober: prober}
	}
	return g
}

// gatedProbingRuntime is gatedRuntime over a runtime that can tell a live agent
// from a pane that merely exists.
type gatedProbingRuntime struct {
	gatedRuntime
	prober ports.AgentLivenessProber
}

var _ ports.AgentLivenessProber = gatedProbingRuntime{}

// AgentAlive forwards to the wrapped runtime's own probe.
func (g gatedProbingRuntime) AgentAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	return g.prober.AgentAlive(ctx, handle)
}

// SendMessage holds until the target pane has been quiet (no user keystrokes) for
// the gate's quiet window — or the max-defer cap elapses, or ctx is cancelled —
// then injects the message onto what is now the user's empty input line.
func (g gatedRuntime) SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error {
	g.gate.WaitForQuiet(ctx, handle.ID)
	return g.Runtime.SendMessage(ctx, handle, message)
}

// agentLivenessProber returns rt's agent-liveness capability, or nil when the
// runtime cannot report it (conpty). Without it, queued-message delivery
// downgrades from "wait for the agent" to "wait a while and hope".
func agentLivenessProber(rt runtimeselect.Runtime) ports.AgentLivenessProber {
	prober, ok := rt.(ports.AgentLivenessProber)
	if !ok {
		return nil
	}
	return prober
}
