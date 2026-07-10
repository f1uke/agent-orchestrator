//go:build unix

package sessionmanager

import "syscall"

// killStray sends SIGTERM to pid — the best-effort straggler termination the
// reaper relies on. Split into a build-tagged file because syscall.Kill/SIGTERM
// do not exist on Windows.
func killStray(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}
