//go:build windows

package releaseweb

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// terminate has no SIGTERM to send on Windows, so it ends the process outright.
func terminate(pid int) error { return kill(pid) }

func kill(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

func processStartedAt(pid int) (time.Time, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // G115: a Windows pid is a DWORD; callers pass os.Getpid or a pid processalive.Alive accepted
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, created.Nanoseconds()), nil
}
