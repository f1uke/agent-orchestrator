//go:build windows

package sessionhost

import (
	"os"
	"time"
)

// Windows has no SIGTERM to offer first.
func terminate(p *os.Process, exited <-chan struct{}, grace time.Duration) {
	select {
	case <-exited:
		return
	default:
	}
	_ = p.Kill()
	waitFor(exited, grace)
}

func exitStatus(ps *os.ProcessState) (code *int, signal *string, ret int) {
	if ps == nil {
		return nil, nil, 1
	}
	c := ps.ExitCode()
	return &c, nil, c
}
