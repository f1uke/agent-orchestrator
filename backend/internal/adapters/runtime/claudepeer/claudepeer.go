// Package claudepeer delivers a message to a claude-code session over the
// session's own unix socket instead of typing it into the session's tmux pane.
//
// Why: the tmux path writes the message into the pane as keystrokes, so it
// competes with whatever the human is typing there, is capped by tmux's
// per-command argv budget, and needs many non-atomic commands for a long
// message. The socket hands the whole message to the agent process directly.
//
// The socket is an UNDOCUMENTED interface with a version on it
// (peerProtocol 1, Claude Code 2.1.260). It can change or vanish in any
// update, so this adapter is built as an optimisation over the tmux runtime,
// never a replacement: it wraps a delegate runtime, overrides only
// SendMessage, and hands the message back to the delegate the moment anything
// about the socket path is unfamiliar, unavailable, or merely uncertain.
// Delivering the message always outranks delivering it the tidy way.
package claudepeer

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Delegate is the runtime this adapter wraps and falls back to. It is the
// tmux runtime in practice; the interface is stated here so the package does
// not import runtimeselect (which imports this one).
type Delegate interface {
	ports.Runtime
	ports.Attacher
	ports.AgentLivenessProber
	SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error
	GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error)
}

// disableEnv turns the socket path off entirely for a machine. It exists
// because the protocol is undocumented: if a Claude Code release starts
// behaving oddly, an operator can pin AO back to the tmux path without
// waiting for a new AO build. Any value other than "0"/"false" leaves the
// socket path on.
const disableEnv = "AO_CLAUDE_NATIVE_SEND"

// Options configures a Runtime. The zero value is usable.
type Options struct {
	Logger *slog.Logger
	// Registry finds the live claude-code session descriptor for a tmux
	// session. Defaults to the real ~/.claude/sessions reader; tests inject.
	Registry Registry
	// Dial opens a connection to a session's messaging socket. Defaults to a
	// plain unix dial; tests inject to drive the failure paths.
	Dial Dialer
	// Now is the clock the guard mirror runs on. Defaults to time.Now.
	Now func() time.Time
}

// Registry resolves a tmux session name to the claude-code session listening
// in that pane, or reports that there is no usable one.
type Registry interface {
	Lookup(ctx context.Context, tmuxSession string) (Session, error)
}

// Runtime is a Delegate whose SendMessage prefers the claude-code messaging
// socket. Every other method is the delegate's, promoted by embedding, so
// wrapping does not hide an optional capability (notably AgentAlive, which
// callers reach by type assertion).
type Runtime struct {
	Delegate

	log      *slog.Logger
	registry Registry
	dial     Dialer
	now      func() time.Time
	guard    *guard
}

// New wraps delegate. delegate must not be nil.
func New(delegate Delegate, opts Options) *Runtime {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	registry := opts.Registry
	if registry == nil {
		registry = NewFileRegistry(FileRegistryOptions{})
	}
	dialer := opts.Dial
	if dialer == nil {
		dialer = dialUnix
	}
	return &Runtime{
		Delegate: delegate,
		log:      log,
		registry: registry,
		dial:     dialer,
		now:      now,
		guard:    newGuard(now),
	}
}

// SendMessage delivers message to the session's own messaging socket when
// that is safe, and otherwise types it into the pane exactly as before.
//
// "Safe" is deliberately strict, because the socket acknowledges nothing: a
// frame the receiver quietly refuses is indistinguishable, from here, from one
// it acted on. So the adapter only takes the socket when it has positively
// established that the target is a live claude-code session at a protocol
// version it was written against, and that the receiver's own inbound guards
// (duplicate suppression, rate limit) would not swallow the message. Anything
// else - including simply not knowing - goes back to the delegate.
//
// The commit point is a COMPLETE write of the framed bytes. The message is the
// last line of the frame and ends in a newline; the receiver enqueues only
// whole parseable lines and discards a trailing fragment, so a short write
// cannot have delivered anything and a complete write always has. That makes
// "fall back iff the write did not complete" exact: the message lands once, on
// exactly one of the two paths, never on both.
func (r *Runtime) SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error {
	reason, err := r.trySocket(ctx, handle, message)
	if err == nil {
		return nil
	}
	if !errors.Is(err, errFellBack) {
		// A real transport failure, not a "this is not for us" verdict.
		r.log.Warn("claude peer socket send failed; falling back to tmux",
			"session", handle.ID, "reason", reason, "error", err)
	} else {
		r.log.Debug("claude peer socket not used; delivering through the pane",
			"session", handle.ID, "reason", reason)
	}
	return r.Delegate.SendMessage(ctx, handle, message)
}

// errFellBack marks a verdict of "this send is not for the socket", as
// distinct from a socket that was tried and failed. Both fall back; only the
// second is worth a warning.
var errFellBack = errors.New("claudepeer: not delivered over the messaging socket")

// trySocket returns nil when the message was committed to the socket. Its
// first return is a short, stable reason string for the log.
func (r *Runtime) trySocket(ctx context.Context, handle ports.RuntimeHandle, message string) (string, error) {
	if disabled() {
		return "disabled-by-env", errFellBack
	}
	if handle.ID == "" {
		return "empty-handle", errFellBack
	}
	session, err := r.registry.Lookup(ctx, handle.ID)
	if err != nil {
		return lookupReason(err), errFellBack
	}
	frame, err := buildFrame(session, message)
	if err != nil {
		return "frame-rejected", errFellBack
	}
	// Mirror the receiver's inbound guards before spending a connection: a
	// message they would drop must go through the pane instead, or it is lost
	// with nobody the wiser.
	release, ok := r.guard.admit(session.SessionID, message)
	if !ok {
		return "receiver-guard-would-drop", errFellBack
	}
	if err := writeFrame(ctx, r.dial, session.SocketPath, frame); err != nil {
		release(false)
		return "socket-write", err
	}
	release(true)
	r.log.Info("delivered message over the claude peer socket",
		"session", handle.ID, "pid", session.PID, "bytes", len(frame))
	return "socket", nil
}

func disabled() bool {
	switch os.Getenv(disableEnv) {
	case "0", "false", "FALSE", "no":
		return true
	default:
		return false
	}
}
