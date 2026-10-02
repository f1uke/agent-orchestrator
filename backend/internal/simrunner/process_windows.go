//go:build windows

package simrunner

import (
	"errors"
	"os/exec"
)

// The runner needs Xcode, which no Windows machine has: Build refuses with
// ErrUnavailable long before either of these is reached. They exist so the
// daemon compiles.

func isolateProcessGroup(*exec.Cmd) {}

func signalGroup(int, bool) error {
	return errors.New("simrunner: the XCTest runner needs macOS")
}
