//go:build !windows

package process

import (
	"os/exec"
	"syscall"
)

// Detach puts cmd's child in a new session, so it outlives the command that
// started it and the terminal's SIGINT and SIGHUP do not reach it.
func Detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
