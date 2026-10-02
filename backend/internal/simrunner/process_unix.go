//go:build !windows

package simrunner

import (
	"os/exec"
	"syscall"
)

// isolateProcessGroup puts xcodebuild in a group of its own, so a Ctrl-C aimed
// at a foreground daemon does not reach it, and so stopping it reaches every
// helper it started.
func isolateProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalGroup signals the process group led by pid - the one AO started, by
// the pid it captured when it started it.
func signalGroup(pid int, kill bool) error {
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	return syscall.Kill(-pid, sig)
}
