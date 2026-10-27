//go:build windows

package releaseweb

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// terminate has no SIGTERM to send on Windows, so it ends the process outright.
func terminate(pid int) error { return kill(pid) }

func kill(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

func processStartedAt(int) (time.Time, error) {
	return time.Time{}, errors.New("release web: process start time is not read on Windows")
}
