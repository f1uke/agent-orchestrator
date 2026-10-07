package processtree

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Parent returns the parent pid of pid.
func Parent(pid int) (int, error) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, fmt.Errorf("processtree: pid %d: %w", pid, err)
	}
	// "pid (comm) state ppid ...": comm may hold spaces and parentheses, so the
	// fields are counted from the LAST closing parenthesis.
	s := string(stat)
	end := strings.LastIndexByte(s, ')')
	if end < 0 {
		return 0, fmt.Errorf("processtree: pid %d: malformed stat %q", pid, s)
	}
	fields := strings.Fields(s[end+1:])
	if len(fields) < 2 {
		return 0, fmt.Errorf("processtree: pid %d: malformed stat %q", pid, s)
	}
	return strconv.Atoi(fields[1])
}
