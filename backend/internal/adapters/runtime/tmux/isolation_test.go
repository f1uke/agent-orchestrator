package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// privateSocketDir returns a fresh directory for the tmux sockets of a test that
// drives a real tmux, and kills every server in it at cleanup.
//
// Every server such a test starts must live here and be reached by an explicit
// -S path. Never tmux's default socket and never `TMUX_TMPDIR` alone: inside an
// AO pane `$TMUX` names the server hosting that pane, a client that sees it
// ignores `TMUX_TMPDIR`, and a cleanup `kill-server` then takes the user's live
// sessions with it (the 2026-10-01 incident).
//
// The directory is made with a short prefix because a socket path has to fit in
// sun_path (see maxSocketPathLen).
func privateSocketDir(t *testing.T) string {
	t.Helper()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux not on PATH: %v", err)
	}
	dir, err := os.MkdirTemp("", "aotm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			// Explicit -S on a socket inside this test's own directory: it
			// cannot address any other server.
			_ = tmuxCommand(tmuxPath, filepath.Join(dir, e.Name()), "kill-server").Run()
		}
		_ = os.RemoveAll(dir)
	})
	return dir
}

// tmuxCommand builds `tmux -S <sock> <args...>` with tmux's pane variables
// dropped from its environment, for a test that talks to a private server
// directly rather than through the runtime.
func tmuxCommand(tmuxPath, sock string, args ...string) *exec.Cmd {
	cmd := exec.Command(tmuxPath, append([]string{"-S", sock}, args...)...)
	cmd.Env = stripEnvKeys(os.Environ(), tmuxClientEnvKeys)
	return cmd
}
