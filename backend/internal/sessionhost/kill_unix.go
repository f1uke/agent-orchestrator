//go:build !windows

package sessionhost

import (
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func terminate(p *os.Process, exited <-chan struct{}, grace time.Duration) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		select {
		case <-exited:
			return
		default:
		}
		_ = syscall.Kill(-p.Pid, sig)
		if waitFor(exited, grace) {
			return
		}
	}
}

func exitStatus(ps *os.ProcessState) (code *int, signal *string, ret int) {
	if ps == nil {
		return nil, nil, 1
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		name := unix.SignalName(ws.Signal())
		return nil, &name, 128 + int(ws.Signal())
	}
	c := ps.ExitCode()
	return &c, nil, c
}
