package simvideo

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// The sweep is the one path that cannot be tested through the Runner seam: it
// signals a pid read off disk, left by a daemon that no longer exists, so there
// is no handle to fake. This spawns a real, harmless process and asserts the
// signal actually reached it - which is the only way to know the reaper does
// anything at all.
type sleeper struct {
	pid int
	cmd *exec.Cmd
	// done is CLOSED rather than sent on, so both the assertion and the cleanup
	// can wait on it. A one-shot send would let whichever read it first starve
	// the other, which is a deadlock in the test rather than in the code.
	done chan struct{}
}

func startSleeper(t *testing.T) *sleeper {
	t.Helper()
	// `sleep` terminates on SIGINT by default, which is all this needs to
	// observe: a signal that arrived.
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn a process to reap on this machine: %v", err)
	}
	s := &sleeper{pid: cmd.Process.Pid, cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(s.done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-s.done
	})
	return s
}

// interrupted reports whether the process has gone away, waiting briefly
// because a signal and an exit are not the same instant.
func (s *sleeper) interrupted(t *testing.T) bool {
	t.Helper()
	select {
	case <-s.done:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}
