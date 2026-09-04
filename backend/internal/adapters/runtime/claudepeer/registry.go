package claudepeer

import (
	"context"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/claudesessions"
)

// supportedPeerProtocol is the only wire version this adapter was written and
// verified against (Claude Code 2.1.260). A session advertising anything else
// is left to the tmux path rather than guessed at: the frames are undocumented,
// and a wrong guess breaks message delivery for every claude-code worker.
const supportedPeerProtocol = 1

// Session is a live claude-code session that can be messaged over its socket.
type Session struct {
	PID        int
	SessionID  string
	SocketPath string
	// PeerToken is the session's inbound auth token, read from its key file.
	// Empty when the key is unreadable, which is not fatal on unix: the
	// receiver only requires auth on Windows. It is never logged.
	PeerToken string
}

// lookupReason is the short, stable reason a send went through the pane
// instead. Every lookup failure is a fallback, never an error the caller
// surfaces.
func lookupReason(err error) string { return claudesessions.Reason(err) }

func reject(reason string) error { return claudesessions.NotFound(reason) }

// FileRegistryOptions configures a FileRegistry. The zero value reads the real
// session registry.
type FileRegistryOptions struct {
	// Dir is the session-registry directory. Defaults to
	// $CLAUDE_CONFIG_DIR/sessions, else ~/.claude/sessions.
	Dir string
	// ProcArgv reports a pid's argv. Defaults to ps(1).
	ProcArgv func(ctx context.Context, pid int) ([]string, error)
	// PIDAlive reports whether a pid exists. Defaults to signal 0.
	PIDAlive func(pid int) bool
}

// FileRegistry answers which claude-code session owns a tmux pane, and whether
// AO may message it over its socket. Finding the session is
// claudesessions' job; deciding it is SAFE to message is this type's.
type FileRegistry struct {
	inner *claudesessions.Registry
}

// NewFileRegistry returns a registry over the real session directory.
func NewFileRegistry(opts FileRegistryOptions) *FileRegistry {
	return &FileRegistry{inner: claudesessions.New(claudesessions.Options{
		Dir:      opts.Dir,
		ProcArgv: opts.ProcArgv,
		PIDAlive: opts.PIDAlive,
	})}
}

// Lookup finds the single live, messageable claude-code session whose pane
// belongs to the given tmux session. Every "no" names a reason, so the daemon
// log says why a send went through the pane instead.
func (r *FileRegistry) Lookup(ctx context.Context, tmuxSession string) (Session, error) {
	session, err := r.inner.ByTmuxSession(ctx, tmuxSession)
	if err != nil {
		return Session{}, err
	}
	if session.PeerProtocol != supportedPeerProtocol {
		return Session{}, reject("unsupported-peer-protocol")
	}
	if session.Kind != "interactive" {
		return Session{}, reject("non-interactive-session")
	}
	if session.SocketPath == "" {
		return Session{}, reject("incomplete-descriptor")
	}
	if launchedIntoBypass(session.Argv) {
		// A session that can be in bypassPermissions PARKS peer messages for
		// the human to approve instead of delivering them, and tells the
		// sender nothing. The launch flag is the superset of "might be in
		// bypass right now" - bypass is not reachable from the mode cycle
		// without it - so refusing on the flag keeps delivery honest.
		return Session{}, reject("bypass-permissions-session")
	}
	if err := socketUsable(session.SocketPath); err != nil {
		return Session{}, err
	}
	return Session{
		PID:        session.PID,
		SessionID:  session.SessionID,
		SocketPath: session.SocketPath,
		PeerToken:  r.inner.PeerToken(session),
	}, nil
}

// launchedIntoBypass reports whether the session was started such that
// bypassPermissions is its mode, or is reachable from its mode cycle. Tokens
// are matched exactly, so a flag quoted inside a long --append-system-prompt
// can at worst cost a fallback to the pane, never a wrong delivery.
func launchedIntoBypass(argv []string) bool {
	for i, arg := range argv {
		switch {
		case arg == "--dangerously-skip-permissions",
			arg == "--permission-mode=bypassPermissions":
			return true
		case arg == "--permission-mode" && i+1 < len(argv) && argv[i+1] == "bypassPermissions":
			return true
		}
	}
	return false
}

func socketUsable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return reject("socket-missing")
	}
	if info.Mode()&os.ModeSocket == 0 {
		return reject("socket-path-not-a-socket")
	}
	return nil
}
