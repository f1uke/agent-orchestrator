//go:build !windows

package releaseweb

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

func kill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }

func processStartedAt(pid int) (time.Time, error) {
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, err
	}
	return time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.Join(strings.Fields(string(out)), " "), time.Local)
}
