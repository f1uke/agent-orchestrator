// Package claudesessions reads the registry Claude Code keeps of its own
// running sessions.
//
// Every interactive Claude Code process writes ~/.claude/sessions/<pid>.json
// describing itself, including the tmux pane it owns and the name it goes by in
// its own UI. Two things in AO need to read it: delivering a message over a
// session's socket (adapters/runtime/claudepeer), and recognising a Claude
// session name where an AO session id was expected (service/session).
//
// The registry is an undocumented file format that a Claude Code release may
// change at any time, so every lookup here fails soft: it returns a
// *NotFoundError naming a short, stable reason, and callers are expected to
// carry on with whatever they would have done without it.
package claudesessions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Session is one live claude-code session as its own registry describes it.
type Session struct {
	PID int
	// SessionID is Claude's conversation id, not an AO session id.
	SessionID string
	// Name is what Claude calls this session in its own UI. It is derived from
	// the worktree directory name plus a random suffix unless the user renamed
	// it, so it is NOT stable and NOT an AO identifier.
	Name string
	// TmuxSession is the tmux SESSION name owned by this process, which is what
	// AO carries as a runtime handle id. It is the only reliable join between
	// the two registries: a crew's two agents share a worktree, so cwd cannot
	// tell them apart, but their panes can.
	TmuxSession  string
	SocketPath   string
	PeerProtocol int
	Kind         string
	// Argv is the process command line as ps(1) flattens it, so a caller can
	// see how the session was launched.
	Argv []string
}

// NotFoundError is why a lookup did not produce a session. Reason is short and
// stable enough to log or assert on.
type NotFoundError struct{ Reason string }

func (e *NotFoundError) Error() string { return "claudesessions: " + e.Reason }

// NotFound builds a lookup failure with the given reason.
func NotFound(reason string) error { return &NotFoundError{Reason: reason} }

// Reason extracts a lookup failure's reason, or a generic label.
func Reason(err error) string {
	var nf *NotFoundError
	if errors.As(err, &nf) {
		return nf.Reason
	}
	return "lookup-failed"
}

// Options configures a Registry. The zero value reads the real registry.
type Options struct {
	// Dir is the session-registry directory. Defaults to
	// $CLAUDE_CONFIG_DIR/sessions, else ~/.claude/sessions.
	Dir string
	// ProcArgv reports a pid's command line. Defaults to ps(1).
	ProcArgv func(ctx context.Context, pid int) ([]string, error)
	// PIDAlive reports whether a pid exists. Defaults to signal 0.
	PIDAlive func(pid int) bool
}

// Registry reads Claude Code's on-disk session registry.
type Registry struct {
	dir   string
	argv  func(ctx context.Context, pid int) ([]string, error)
	alive func(pid int) bool
}

// New returns a registry over the configured (by default, the real) directory.
func New(opts Options) *Registry {
	dir := opts.Dir
	if dir == "" {
		dir = DefaultDir()
	}
	argv := opts.ProcArgv
	if argv == nil {
		argv = psArgv
	}
	alive := opts.PIDAlive
	if alive == nil {
		alive = pidAlive
	}
	return &Registry{dir: dir, argv: argv, alive: alive}
}

// DefaultDir is where Claude Code keeps the registry.
func DefaultDir() string {
	if base := os.Getenv("CLAUDE_CONFIG_DIR"); base != "" {
		return filepath.Join(base, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "sessions")
}

// descriptor is the subset of <pid>.json this package reads.
type descriptor struct {
	PID          int    `json:"pid"`
	SessionID    string `json:"sessionId"`
	Name         string `json:"name"`
	PeerProtocol int    `json:"peerProtocol"`
	Kind         string `json:"kind"`
	Tmux         string `json:"tmux"`
	SocketPath   string `json:"messagingSocketPath"`
}

var descriptorName = regexp.MustCompile(`^\d+\.json$`)

// ByTmuxSession finds the live session that owns a pane in the named tmux
// session. This is the lookup AO's runtime handle joins on.
func (r *Registry) ByTmuxSession(ctx context.Context, tmuxSession string) (Session, error) {
	if tmuxSession == "" {
		return Session{}, NotFound("no-tmux-session")
	}
	return r.lookup(ctx, func(d descriptor) bool {
		return TmuxSessionName(d.Tmux) == tmuxSession
	})
}

// ByName finds the live session Claude Code calls `name` in its own UI. The
// match is exact first and case-insensitive only as a fallback, because a
// user-renamed session keeps the case the user typed.
func (r *Registry) ByName(ctx context.Context, name string) (Session, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Session{}, NotFound("no-name")
	}
	session, err := r.lookup(ctx, func(d descriptor) bool { return d.Name == name })
	if err == nil || Reason(err) != "no-descriptor" {
		return session, err
	}
	return r.lookup(ctx, func(d descriptor) bool { return strings.EqualFold(d.Name, name) })
}

// lookup returns the one live claude-code session matching want. Anything less
// certain than exactly one match is a failure with a reason: guessing between
// two candidates would mean acting on the wrong session.
func (r *Registry) lookup(ctx context.Context, want func(descriptor) bool) (Session, error) {
	if r.dir == "" {
		return Session{}, NotFound("no-sessions-dir")
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return Session{}, NotFound("sessions-dir-unreadable")
	}
	var matches []descriptor
	for _, entry := range entries {
		if entry.IsDir() || !descriptorName.MatchString(entry.Name()) {
			continue
		}
		desc, ok := readDescriptor(filepath.Join(r.dir, entry.Name()))
		if !ok || desc.PID <= 0 || !want(desc) {
			continue
		}
		matches = append(matches, desc)
	}
	switch len(matches) {
	case 0:
		return Session{}, NotFound("no-descriptor")
	case 1:
	default:
		return Session{}, NotFound("ambiguous-descriptor")
	}
	desc := matches[0]

	if !r.alive(desc.PID) {
		return Session{}, NotFound("dead-pid")
	}
	argv, err := r.argv(ctx, desc.PID)
	if err != nil {
		return Session{}, NotFound("proc-unreadable")
	}
	if !IsClaudeProcess(argv) {
		// The pid is alive but is not the process that wrote the record - a
		// recycled pid, or a descriptor a crashed session never cleaned up.
		//
		// The descriptor's `procStart` is deliberately NOT used for this:
		// Claude Code records it in a different timezone from the one ps(1)
		// prints locally, so the two strings never match even for the same
		// live process.
		return Session{}, NotFound("recycled-pid")
	}
	return Session{
		PID:          desc.PID,
		SessionID:    desc.SessionID,
		Name:         desc.Name,
		TmuxSession:  TmuxSessionName(desc.Tmux),
		SocketPath:   desc.SocketPath,
		PeerProtocol: desc.PeerProtocol,
		Kind:         desc.Kind,
		Argv:         argv,
	}, nil
}

// PeerToken reads a session's inbound auth token from the key file Claude Code
// publishes beside the descriptor:
// <dir>/<pid>.<sha256 of the resolved socket path>.key. It returns an empty
// string when the key is unreadable, and the token must never be logged.
func (r *Registry) PeerToken(session Session) string {
	if session.SocketPath == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(filepath.Clean(session.SocketPath)))
	name := strconv.Itoa(session.PID) + "." + hex.EncodeToString(sum[:]) + ".key"
	raw, err := os.ReadFile(filepath.Join(r.dir, name))
	if err != nil {
		return ""
	}
	var key struct {
		PeerToken string `json:"peerToken"`
	}
	if err := json.Unmarshal(raw, &key); err != nil {
		return ""
	}
	return key.PeerToken
}

func readDescriptor(path string) (descriptor, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return descriptor{}, false
	}
	var desc descriptor
	if err := json.Unmarshal(raw, &desc); err != nil {
		return descriptor{}, false
	}
	return desc, true
}

// TmuxSessionName pulls the tmux SESSION out of a descriptor's
// "<session>:@<window>.<pane>" target. AO's runtime handle is that session
// name, which is what makes the two registries join. A session name may itself
// contain a colon, so the pane suffix is the LAST one.
func TmuxSessionName(target string) string {
	if target == "" {
		return ""
	}
	if i := strings.LastIndex(target, ":"); i >= 0 {
		return target[:i]
	}
	return target
}

// IsClaudeProcess reports whether argv belongs to a claude-code process.
func IsClaudeProcess(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	return filepath.Base(argv[0]) == "claude"
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// psArgv reads a pid's argv with one ps(1) call. ps flattens argv into one
// space-separated line, so an argument containing spaces (AO passes a whole
// system prompt) splits into several tokens here. That is fine for both
// questions asked of it: argv[0], and an exact-token flag match.
func psArgv(ctx context.Context, pid int) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil, fmt.Errorf("claudesessions: ps -p %d: %w", pid, err)
	}
	argv := strings.Fields(string(out))
	if len(argv) == 0 {
		return nil, fmt.Errorf("claudesessions: ps reported no command line for pid %d", pid)
	}
	return argv, nil
}
