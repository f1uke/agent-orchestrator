//go:build windows

package sessionmanager

import "os"

// killStray terminates pid on Windows, which has no SIGTERM. os.Process.Kill
// maps to TerminateProcess — the closest best-effort equivalent. In practice the
// reaper is a no-op here anyway: lsofCwdPIDs shells out to `lsof`, which is
// absent on Windows and yields an empty pid list, so this is never reached with
// a live pid. It exists to keep the package building on Windows.
func killStray(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
