package processtree

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// Parent returns the parent pid of pid.
func Parent(pid int) (int, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, fmt.Errorf("processtree: pid %d: %w", pid, err)
	}
	return int(kp.Eproc.Ppid), nil
}
