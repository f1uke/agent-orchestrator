//go:build !windows

package simvideo

import (
	"os"
	"os/exec"
	"syscall"
)

// The two process operations that are not portable. They live here, behind a
// build tag, so the package - and therefore the daemon - still COMPILES on
// Windows, where `syscall.SysProcAttr` has no Setpgid and a process cannot be
// sent SIGINT at all. Nothing is lost by that: recording needs `xcrun`, and a
// machine without it is refused with ErrUnavailable long before either of
// these is reached.

// isolateProcessGroup puts the recorder in its own process group, so a signal
// aimed at the daemon's terminal - a Ctrl-C in a foreground `ao daemon` -
// cannot reach a recorder and kill it the one way that truncates the file.
func isolateProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// interruptPID sends SIGINT, which is the only signal that leaves a playable
// file: simctl writes the container's index when it is interrupted and exits.
//
// Measured on macOS 26.4 / Xcode 26.3, `xcrun` EXECS simctl rather than forking
// it, so the pid we started is the recorder itself and this reaches it directly.
func interruptPID(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGINT)
}
