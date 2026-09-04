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
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/msgdelivery"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgorigin"
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

// modeEnv chooses how hard AO tries to use the socket. It exists because the
// protocol is undocumented: if a Claude Code release starts behaving oddly, an
// operator can pin AO back to the tmux path without waiting for a new AO build,
// and someone hunting fallbacks can make them impossible to miss.
//
//	0 / false / FALSE / no   pane only: never touch the socket
//	strict                   socket only: FAIL the send rather than fall back
//	anything else, or unset  prefer the socket, fall back to the pane
const modeEnv = "AO_CLAUDE_NATIVE_SEND"

// sendMode is what modeEnv resolved to for one send.
type sendMode int

const (
	// modeAuto is the DEFAULT and must stay the default: prefer the socket,
	// deliver through the pane whenever the socket is unfamiliar, unavailable or
	// merely uncertain. Delivering the message outranks delivering it the tidy
	// way - if a future Claude Code release changed the protocol, a forced-socket
	// default would make every message in the system vanish silently, which is
	// far worse than a message being typed at somebody.
	modeAuto sendMode = iota
	// modeOff pins delivery to the pane.
	modeOff
	// modeStrict refuses the pane fallback: a send that cannot take the socket
	// fails, loudly, naming the reason. Opt-in, for someone deliberately hunting
	// fallbacks - it turns "this was quietly typed at me" into an error the
	// caller sees.
	modeStrict
)

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
	// Journal persists which wire each message took. Nil keeps nothing, which
	// is the pre-existing behaviour and what tests use.
	Journal msgdelivery.Journal
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
	journal  msgdelivery.Journal
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
		journal:  opts.Journal,
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
//
// Whichever path it took is REPORTED - to the caller through the context, and
// to the delivery journal - because the decision is made on facts only this
// function has, and the question is asked hours later. See internal/msgdelivery.
//
// Under AO_CLAUDE_NATIVE_SEND=strict the fallback is refused and the send fails
// instead, carrying the same reason. That is opt-in and never the default; see
// modeEnv.
func (r *Runtime) SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error {
	report, err := r.trySocket(ctx, handle, message)
	if err == nil {
		msgdelivery.Record(ctx, r.journal, handle.ID, report)
		return nil
	}
	if !errors.Is(err, errFellBack) {
		// A real transport failure, not a "this is not for us" verdict.
		r.log.Warn("claude peer socket send failed; falling back to tmux",
			"session", handle.ID, "reason", report.Reason, "error", err)
	} else {
		r.log.Debug("claude peer socket not used; delivering through the pane",
			"session", handle.ID, "reason", report.Reason)
	}
	if mode() == modeStrict {
		// Opted out of the fallback: say what was refused and why, and let the
		// caller find out instead of having text typed at somebody.
		report.Path = msgdelivery.PathNone
		strictErr := fmt.Errorf("%w: %s=strict refused the pane fallback and the claude peer socket could not be used (reason=%s): %w",
			msgdelivery.ErrNotDelivered, modeEnv, report.Reason, err)
		report.Error = strictErr.Error()
		r.log.Error("strict mode refused the pane fallback; the message was not delivered",
			"session", handle.ID, "reason", report.Reason, "error", err)
		msgdelivery.Record(ctx, r.journal, handle.ID, report)
		return strictErr
	}
	paneErr := r.Delegate.SendMessage(ctx, handle, message)
	// Recorded only now, after the pane send has actually run: "pane" has to
	// mean the message landed there, exactly as "socket" means the frame was
	// completely written. A pane send that failed delivered nothing either.
	report.Path = msgdelivery.PathPane
	if paneErr != nil {
		report.Path = msgdelivery.PathNone
		report.Error = paneErr.Error()
	}
	msgdelivery.Record(ctx, r.journal, handle.ID, report)
	return paneErr
}

// errFellBack marks a verdict of "this send is not for the socket", as
// distinct from a socket that was tried and failed. Both fall back; only the
// second is worth a warning.
var errFellBack = errors.New("claudepeer: not delivered over the messaging socket")

// trySocket returns nil when the message was committed to the socket. Its first
// return is the account of what happened that the caller reports and persists:
// on a fallback it carries the reason and no path, and the caller fills the path
// in once it knows which one the message really took.
//
// This is the ONE place a reason is decided. Nothing above may re-derive it: a
// plausible wrong reason is worse than none, because it will be believed.
func (r *Runtime) trySocket(ctx context.Context, handle ports.RuntimeHandle, message string) (msgdelivery.Report, error) {
	sender := senderName(ctx)
	fellBack := func(reason string) (msgdelivery.Report, error) {
		return msgdelivery.Report{Reason: reason, Sender: sender}, errFellBack
	}
	if mode() == modeOff {
		return fellBack("disabled-by-env")
	}
	if handle.ID == "" {
		return fellBack("empty-handle")
	}
	session, err := r.registry.Lookup(ctx, handle.ID)
	if err != nil {
		return fellBack(lookupReason(err))
	}
	frame, err := buildFrame(session, message, sender)
	if err != nil {
		return fellBack("frame-rejected")
	}
	report := msgdelivery.Report{
		Reason:      "frame-built",
		Sender:      sender,
		NameOnWire:  frame.nameOnWire,
		NameDropped: frame.nameDropped,
		MsgID:       frame.msgID,
		Bytes:       len(frame.bytes),
	}
	// Mirror the receiver's inbound guards before spending a connection: a
	// message they would drop must go through the pane instead, or it is lost
	// with nobody the wiser.
	release, ok := r.guard.admit(session.SessionID, message)
	if !ok {
		report.Reason = "receiver-guard-would-drop"
		return report, errFellBack
	}
	if err := writeFrame(ctx, r.dial, session.SocketPath, frame.bytes); err != nil {
		release(false)
		report.Reason = "socket-write"
		report.Error = err.Error()
		return report, err
	}
	release(true)
	r.log.Info("delivered message over the claude peer socket",
		"session", handle.ID, "pid", session.PID, "bytes", len(frame.bytes), "sender", sender)
	// The commit point has passed: every byte of the frame reached the socket,
	// which is the only thing that may ever be reported as a socket delivery.
	report.Path = msgdelivery.PathSocket
	report.Reason = ""
	return report, nil
}

// defaultSenderName is what AO calls itself on the wire when no AO session
// authored the message - a human typing in the app, a nudge, a report-back. It
// names the thing that is actually sending, which is the daemon, and it is the
// only name AO ever asserts about itself.
const defaultSenderName = "agent-orchestrator"

// senderName is the name to put on the wire for this send.
//
// It is an ATTRIBUTION, not an authentication. What AO knows is what the caller
// of `ao send` said about itself, the same claim it already prints as
// `[from @<id>]`; the receiver treats a name as sender-asserted display text
// for exactly that reason, and verifies only the connecting pid, which is AO's
// own daemon. When no session authored the message, AO says so by naming
// itself rather than by going anonymous.
func senderName(ctx context.Context) string {
	if session := strings.TrimSpace(msgorigin.Sender(ctx)); session != "" {
		return session
	}
	return defaultSenderName
}

// mode reads modeEnv. It is read per send rather than cached at construction so
// an operator can change it without restarting the daemon - the same property
// the kill switch has always had.
func mode() sendMode {
	value := os.Getenv(modeEnv)
	switch {
	case value == "0" || value == "false" || value == "FALSE" || value == "no":
		return modeOff
	case strings.EqualFold(strings.TrimSpace(value), "strict"):
		return modeStrict
	default:
		return modeAuto
	}
}
