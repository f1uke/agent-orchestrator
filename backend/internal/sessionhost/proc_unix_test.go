//go:build !windows

package sessionhost

import "syscall"

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func killIfAlive(pid int) {
	if pid > 0 && alive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
