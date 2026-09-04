package claudepeer

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

// lookupError carries a short, stable reason for the daemon log. Every lookup
// failure is a fallback, never an error the caller surfaces.
type lookupError struct{ reason string }

func (e *lookupError) Error() string { return "claudepeer: " + e.reason }

func lookupReason(err error) string {
	var le *lookupError
	if errors.As(err, &le) {
		return le.reason
	}
	return "lookup-failed"
}

func reject(reason string) error { return &lookupError{reason: reason} }

// FileRegistryOptions configures a FileRegistry. The zero value reads the real
// session registry.
type FileRegistryOptions struct {
	// Dir is the session-registry directory. Defaults to
	// $CLAUDE_CONFIG_DIR/sessions, else ~/.claude/sessions.
	Dir string
	// ProcInfo reports a pid's argv. Defaults to ps(1).
	ProcInfo func(ctx context.Context, pid int) (procInfo, error)
	// PIDAlive reports whether a pid exists. Defaults to signal 0.
	PIDAlive func(pid int) bool
}

// procInfo is what we can learn about a candidate process without touching it:
// its command line, split into whitespace-separated tokens. It answers two
// questions - is this still a claude-code process, and was it launched into
// bypassPermissions.
//
// The descriptor's `procStart` is deliberately NOT used to detect a recycled
// pid: Claude Code records it in a different timezone from the one ps(1)
// prints locally, so the two strings never match even for the same live
// process. Identity comes from the argv check instead.
type procInfo struct {
	Argv []string
}

// FileRegistry reads Claude Code's on-disk session registry.
type FileRegistry struct {
	dir      string
	procInfo func(ctx context.Context, pid int) (procInfo, error)
	alive    func(pid int) bool
}

// NewFileRegistry returns a registry over the real session directory.
func NewFileRegistry(opts FileRegistryOptions) *FileRegistry {
	dir := opts.Dir
	if dir == "" {
		dir = defaultSessionsDir()
	}
	info := opts.ProcInfo
	if info == nil {
		info = psProcInfo
	}
	alive := opts.PIDAlive
	if alive == nil {
		alive = pidAlive
	}
	return &FileRegistry{dir: dir, procInfo: info, alive: alive}
}

func defaultSessionsDir() string {
	if base := os.Getenv("CLAUDE_CONFIG_DIR"); base != "" {
		return filepath.Join(base, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "sessions")
}

// descriptor is the subset of ~/.claude/sessions/<pid>.json this adapter reads.
type descriptor struct {
	PID          int    `json:"pid"`
	SessionID    string `json:"sessionId"`
	PeerProtocol int    `json:"peerProtocol"`
	Kind         string `json:"kind"`
	Tmux         string `json:"tmux"`
	SocketPath   string `json:"messagingSocketPath"`
}

var descriptorName = regexp.MustCompile(`^\d+\.json$`)

// Lookup finds the single live, messageable claude-code session whose pane
// belongs to the given tmux session. Every "no" is a *lookupError naming the
// reason, so the daemon log says why a send went through the pane instead.
func (r *FileRegistry) Lookup(ctx context.Context, tmuxSession string) (Session, error) {
	if r.dir == "" {
		return Session{}, reject("no-sessions-dir")
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return Session{}, reject("sessions-dir-unreadable")
	}
	var matches []descriptor
	for _, entry := range entries {
		if entry.IsDir() || !descriptorName.MatchString(entry.Name()) {
			continue
		}
		desc, ok := readDescriptor(filepath.Join(r.dir, entry.Name()))
		if !ok || tmuxSessionName(desc.Tmux) != tmuxSession {
			continue
		}
		matches = append(matches, desc)
	}
	switch len(matches) {
	case 0:
		return Session{}, reject("no-descriptor")
	case 1:
	default:
		// Two records claiming one pane: we cannot tell which process is the
		// one in front of the human, and messaging the wrong one is worse than
		// typing into the right one.
		return Session{}, reject("ambiguous-descriptor")
	}
	desc := matches[0]

	if desc.PeerProtocol != supportedPeerProtocol {
		return Session{}, reject("unsupported-peer-protocol")
	}
	if desc.Kind != "interactive" {
		return Session{}, reject("non-interactive-session")
	}
	if desc.SocketPath == "" || desc.PID <= 0 {
		return Session{}, reject("incomplete-descriptor")
	}
	if !r.alive(desc.PID) {
		return Session{}, reject("dead-pid")
	}
	info, err := r.procInfo(ctx, desc.PID)
	if err != nil {
		return Session{}, reject("proc-unreadable")
	}
	if !isClaudeProcess(info.Argv) {
		// The pid is alive but is not the process that wrote the record - a
		// recycled pid, or a descriptor a crashed session never cleaned up.
		return Session{}, reject("recycled-pid")
	}
	if launchedIntoBypass(info.Argv) {
		// A session that can be in bypassPermissions PARKS peer messages for
		// the human to approve instead of delivering them, and tells the
		// sender nothing. The launch flag is the superset of "might be in
		// bypass right now" - bypass is not reachable from the mode cycle
		// without it - so refusing on the flag keeps delivery honest.
		return Session{}, reject("bypass-permissions-session")
	}
	if err := socketUsable(desc.SocketPath); err != nil {
		return Session{}, err
	}
	return Session{
		PID:        desc.PID,
		SessionID:  desc.SessionID,
		SocketPath: desc.SocketPath,
		PeerToken:  readPeerToken(r.dir, desc.PID, desc.SocketPath),
	}, nil
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

// tmuxSessionName pulls the tmux SESSION out of a descriptor's
// "<session>:@<window>.<pane>" target. AO's runtime handle is that session
// name, which is what makes the two registries join.
func tmuxSessionName(target string) string {
	if target == "" {
		return ""
	}
	if i := strings.LastIndex(target, ":"); i >= 0 {
		return target[:i]
	}
	return target
}

// isClaudeProcess reports whether argv belongs to a claude-code process. It is
// how a live-but-recycled pid is told apart from the session that wrote the
// descriptor.
func isClaudeProcess(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	return filepath.Base(argv[0]) == "claude"
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

// readPeerToken reads the session's inbound token from the key file Claude
// Code publishes beside the descriptor:
// <sessions dir>/<pid>.<sha256 of the resolved socket path>.key. The token is
// returned to the caller and never logged, and an unreadable key is a
// best-effort miss, not a failure: on unix the receiver does not require auth.
func readPeerToken(dir string, pid int, socketPath string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(socketPath)))
	name := strconv.Itoa(pid) + "." + hex.EncodeToString(sum[:]) + ".key"
	raw, err := os.ReadFile(filepath.Join(dir, name))
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

// psProcInfo reads a pid's argv with one ps(1) call. ps flattens argv into one
// space-separated line, so an argument containing spaces (AO passes a whole
// system prompt) splits into several tokens here. That is fine for both
// questions asked of it: an exact-token flag match and argv[0].
func psProcInfo(ctx context.Context, pid int) (procInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return procInfo{}, fmt.Errorf("claudepeer: ps -p %d: %w", pid, err)
	}
	argv := strings.Fields(string(out))
	if len(argv) == 0 {
		return procInfo{}, fmt.Errorf("claudepeer: ps reported no command line for pid %d", pid)
	}
	return procInfo{Argv: argv}, nil
}
