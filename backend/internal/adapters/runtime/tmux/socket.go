package tmux

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ONE TMUX SERVER PER SESSION.
//
// Every AO session runs on its own tmux server, reached through an explicit
// socket under the data dir (`<dataDir>/tmux/<session-name>`), never through
// tmux's default socket.
//
// The reason is `$TMUX`. tmux exports it into every pane, pointing at the server
// that hosts the pane, and a tmux client that sees it talks to THAT server and
// ignores `-L`-style resolution and `TMUX_TMPDIR`. So anything an agent runs in
// its pane - `TMUX_TMPDIR=/sandbox tmux kill-server` included - addresses the
// server its own pane lives on. While every AO session shared one server (the
// user's default one), a single such command ended every session of every
// project at once (2026-10-01: sixteen sessions in one second). With a server
// per session, the most any pane can take down is its own session.
//
// The socket lives under the data dir for the second half of the isolation: an
// AO instance with its own data dir (a sandbox, an e2e run) gets its own servers
// without anyone remembering to set TMUX_TMPDIR, and its sessions can never
// share a name with the real instance's.
//
// `$TMUX` itself stays in the pane: Claude Code reads it to record which tmux
// session owns it, and claudepeer delivery joins AO's handle on that record.

// maxSocketPathLen is the longest unix socket path every supported platform
// accepts: sun_path is 104 bytes on macOS (108 on Linux), NUL included. tmux
// refuses a longer -S path outright.
const maxSocketPathLen = 103

// hashedSocketNameLen is the length of the fallback socket file name
// SocketPathIn uses when a session name does not fit.
const hashedSocketNameLen = len("s-") + 16

// SocketDir returns the directory holding the tmux sockets of the AO instance
// whose data dir is dataDir: `<dataDir>/tmux`. When that is too long to leave
// room for a socket name (a data dir deep under a temp dir), it falls back to a
// short directory under the OS temp dir, keyed by the data dir so two instances
// never share it. An empty dataDir (a runtime built without one) gets a
// per-user directory under the OS temp dir.
func SocketDir(dataDir string) string {
	if dataDir == "" {
		return filepath.Join(os.TempDir(), fmt.Sprintf("ao-tmux-%d", os.Getuid()))
	}
	dir := filepath.Join(dataDir, "tmux")
	if len(dir)+1+hashedSocketNameLen <= maxSocketPathLen {
		return dir
	}
	sum := sha256.Sum256([]byte(dir))
	return filepath.Join(os.TempDir(), "ao-tmux-"+hex.EncodeToString(sum[:6]))
}

// SocketPath returns the socket of the tmux server hosting session name for the
// AO instance whose data dir is dataDir. It is what a person needs to attach by
// hand: `tmux -S <SocketPath> attach -t <name>`.
func SocketPath(dataDir, name string) string {
	return SocketPathIn(SocketDir(dataDir), name)
}

// SocketPathIn names a session's socket inside dir: the session name itself, so
// `ls` of the directory reads like `tmux ls` did, or a hash of it when the name
// would push the path past maxSocketPathLen.
func SocketPathIn(dir, name string) string {
	if p := filepath.Join(dir, name); len(p) <= maxSocketPathLen {
		return p
	}
	sum := sha256.Sum256([]byte(name))
	return filepath.Join(dir, "s-"+hex.EncodeToString(sum[:8]))
}

// LegacyDefaultSocket returns the socket of tmux's default server - where AO ran
// every session before each got its own server: `$TMUX_TMPDIR` (else /tmp),
// then `tmux-<uid>/default`. The runtime still reaches sessions there that this
// instance created before the upgrade (see Options.LegacySocket), so they are
// neither orphaned nor relaunched on top of their still-running agent.
func LegacyDefaultSocket() string {
	base := os.Getenv("TMUX_TMPDIR")
	if base == "" {
		base = "/tmp"
	}
	return filepath.Join(base, fmt.Sprintf("tmux-%d", os.Getuid()), "default")
}

// SweepStaleSockets removes the sockets in dir that no server listens on any
// more. tmux leaves its socket file behind when a server exits, so without this
// the directory would keep one file per session name ever used. Only a socket
// that REFUSES a connection is removed - a live server, or one that cannot be
// asked, is left alone - and it is meant for daemon start, when no Create can be
// racing to bind the same path.
func SweepStaleSockets(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Type()&os.ModeSocket == 0 {
			continue
		}
		p := filepath.Join(dir, e.Name())
		conn, err := net.DialTimeout("unix", p, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			continue
		}
		if errors.Is(err, syscall.ECONNREFUSED) {
			_ = os.Remove(p)
		}
	}
}

// ownSocket is the socket of session id's own server.
func (r *Runtime) ownSocket(id string) string {
	return SocketPathIn(r.socketDir, id)
}

// socketFor returns the socket of the server hosting session id: its own,
// unless it is a session this instance left on the legacy shared server (see
// legacyHosts). An error means the answer is not known - a caller must treat it
// as a failed probe, never as "the session is gone".
func (r *Runtime) socketFor(ctx context.Context, id string) (string, error) {
	legacy, err := r.legacyHosts(ctx, id)
	if err != nil {
		return "", err
	}
	if legacy {
		return r.legacySocket, nil
	}
	return r.ownSocket(id), nil
}

// legacyHosts reports whether session id lives on the legacy shared server.
// Only when the fallback is on and the session has no socket of its own; then
// the legacy server is asked once and the definitive answer cached. Nothing
// creates sessions there any more, so an answer cannot go stale except by the
// session ending, which the next command against it reports anyway.
func (r *Runtime) legacyHosts(ctx context.Context, id string) (bool, error) {
	if r.legacySocket == "" {
		return false, nil
	}
	if _, err := os.Stat(r.ownSocket(id)); err == nil {
		return false, nil
	}
	if v, ok := r.legacyOwned.Load(id); ok {
		owned, _ := v.(bool)
		return owned, nil
	}
	owned, err := r.legacyOwns(ctx, id)
	if err != nil {
		return false, err
	}
	r.legacyOwned.Store(id, owned)
	return owned, nil
}

// legacyOwns asks the legacy server whether it hosts session id AND this
// instance created it. The proof of ownership is the pane's start command: the
// runtime launches every pane as `<shell> <dataDir>/runtime/launch/launch-<id>-*.sh`
// (see launchInvocation), so a session another AO instance - a sandbox, an e2e
// run - put on the same shared server never matches, even under the same name.
// A legacy server that is not running is a definitive "no".
func (r *Runtime) legacyOwns(ctx context.Context, id string) (bool, error) {
	if r.dataDir == "" {
		return false, nil
	}
	out, err := r.run(ctx, r.legacySocket, listAllPaneStartsArgs()...)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && sessionMissingOutput(string(out)) {
			return false, nil
		}
		return false, fmt.Errorf("probe legacy tmux server %s: %w", r.legacySocket, err)
	}
	stamp := filepath.Join(r.dataDir, "runtime", "launch", "launch-"+id+"-")
	for _, line := range strings.Split(string(out), "\n") {
		name, start, ok := strings.Cut(line, "\t")
		if ok && name == id && startsScript(start, stamp) {
			return true, nil
		}
	}
	return false, nil
}

// startsScript reports whether a pane start command (`<shell> <script>`) runs a
// script whose path begins with prefix. The path has to start a word - after a
// space or an opening quote tmux may add - so a launch dir that merely ENDS the
// same way (`/sandbox/.ao/data/...` against `/data/...`) is not taken for it.
func startsScript(start, prefix string) bool {
	for i := 0; ; {
		j := strings.Index(start[i:], prefix)
		if j < 0 {
			return false
		}
		at := i + j
		if at == 0 || strings.ContainsRune(" \t'\"", rune(start[at-1])) {
			return true
		}
		i = at + 1
	}
}

// retireLegacy clears the way for Create to start session id on its own server
// when this instance still has a same-named session on the legacy server: a
// stale one (its agent gone) is killed, a live one refuses the create - the same
// rule Create applies to a duplicate on its own server. Either way the id then
// resolves to its own socket.
//
// A cached answer is only a shortcut, so any failure here forgets it: the next
// Create asks the shared server afresh instead of repeating the same doomed
// probe for the rest of the daemon's life (which made a session's name
// unlaunchable until the daemon restarted).
func (r *Runtime) retireLegacy(ctx context.Context, id string) (err error) {
	defer func() {
		if err != nil {
			r.legacyOwned.Delete(id)
		}
	}()
	legacy, err := r.legacyHosts(ctx, id)
	if err != nil {
		return fmt.Errorf("tmux runtime: create session %s: %w", id, err)
	}
	if !legacy {
		return nil
	}
	alive, err := r.agentAliveOn(ctx, r.legacySocket, id)
	if err != nil {
		return fmt.Errorf("tmux runtime: create session %s: probe its pre-upgrade session: %w", id, err)
	}
	if alive {
		return fmt.Errorf("tmux runtime: create session %s: a live agent already occupies that name on the shared tmux server %s", id, r.legacySocket)
	}
	if err := r.destroyOn(ctx, r.legacySocket, id); err != nil {
		return fmt.Errorf("tmux runtime: create session %s: reap its pre-upgrade session: %w", id, err)
	}
	r.legacyOwned.Store(id, false)
	return nil
}
