//go:build windows

package simvideo

import (
	"errors"
	"os/exec"
)

// Windows has no process groups of this shape and no way to deliver SIGINT to
// another process, so neither operation is implemented. This is not a gap: a
// simulator recording needs `xcrun`, which no Windows machine has, so Start
// refuses with ErrUnavailable and nothing here is ever called. It exists so the
// daemon compiles.

// isolateProcessGroup is a no-op: there is no recorder to isolate.
func isolateProcessGroup(*exec.Cmd) {}

// interruptPID always fails, and says why rather than pretending it signalled
// something - a silent success here would mean a caller waiting forever for a
// file nothing is writing.
func interruptPID(int) error {
	return errors.New("simvideo: a recorder cannot be interrupted on Windows; iOS Simulator recording needs macOS")
}
