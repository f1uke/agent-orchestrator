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
func newGatedRuntime(inner runtimeselect.Runtime, gate *inputgate.Gate) gatedRuntime {
	return gatedRuntime{Runtime: inner, gate: gate}
}

// SendMessage holds until the target pane has been quiet (no user keystrokes) for
// the gate's quiet window — or the max-defer cap elapses, or ctx is cancelled —
// then injects the message onto what is now the user's empty input line.
func (g gatedRuntime) SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error {
	g.gate.WaitForQuiet(ctx, handle.ID)
	return g.Runtime.SendMessage(ctx, handle, message)
}
