// Package runtimeselect picks the correct runtime backend by platform:
// tmux on Darwin/Linux, conpty (ConPTY) on Windows.
//
// On Darwin/Linux the tmux runtime is additionally wrapped by claudepeer,
// which hands a message to a claude-code session over that session's own unix
// socket instead of typing it into the pane, and falls back to tmux for every
// other harness and for anything it is not certain about.
package runtimeselect

import (
	"context"
	"log/slog"
	"runtime"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/claudepeer"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Runtime is the union interface that both tmux and conpty satisfy.
// It extends ports.Runtime (Create/Destroy/IsAlive) with the additional methods
// the daemon wires directly, including ports.Attacher (Attach) so the terminal
// layer can open a Stream against the selected runtime.
type Runtime interface {
	ports.Runtime // Create, Destroy, IsAlive
	ports.Attacher
	SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error
	GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error)
}

// Compile-time assertions: every adapter must implement the union interface.
var _ Runtime = (*tmux.Runtime)(nil)
var _ Runtime = (*conpty.Runtime)(nil)
var _ Runtime = (*claudepeer.Runtime)(nil)

// claudepeer wraps rather than replaces the tmux runtime, so it must still
// carry tmux's optional agent-liveness capability: callers reach AgentAlive by
// type assertion, and a wrapper that swallowed it would silently downgrade
// queued-message delivery.
var _ ports.AgentLivenessProber = (*claudepeer.Runtime)(nil)

// New returns the per-platform runtime: tmux on Darwin/Linux, conpty on Windows.
func New(log *slog.Logger) Runtime {
	if runtime.GOOS == "windows" {
		return conpty.New(conpty.Options{})
	}
	return claudepeer.New(tmux.New(tmux.Options{}), claudepeer.Options{Logger: log})
}
