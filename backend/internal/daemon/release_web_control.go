package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/aoagents/agent-orchestrator/backend/internal/releaseweb"
)

// ReleaseWebControl starts, stops and inspects the team web program, which
// runs as its own process (`ao release-web serve`) and outlives the daemon.
// The CLI and the daemon's HTTP routes share it, so both do the same thing.
type ReleaseWebControl struct {
	Paths      releaseweb.Paths
	Executable func() (string, error)
	// Clipboard builds the command that reads the key on stdin and puts it on
	// the clipboard; nil means this OS has none.
	Clipboard func() *exec.Cmd
}

// NewReleaseWebControl returns the control for the release web that sits
// beside dataDir (see releaseweb.DirFor).
func NewReleaseWebControl(dataDir string) *ReleaseWebControl {
	c := &ReleaseWebControl{
		Paths:      releaseweb.Paths{Dir: releaseweb.DirFor(dataDir)},
		Executable: os.Executable,
	}
	if runtime.GOOS == "darwin" {
		c.Clipboard = func() *exec.Cmd { return exec.Command("pbcopy") }
	}
	return c
}

// Status reports whether the web runs and the state of its key. It never
// carries the key.
func (c *ReleaseWebControl) Status(context.Context) (releaseweb.Status, error) {
	return releaseweb.ReadStatus(c.Paths), nil
}

// EnsureKey creates the first key when none exists and reports whether it did.
// A key file that exists but is unusable is an error, never overwritten.
func (c *ReleaseWebControl) EnsureKey() (created bool, err error) {
	_, err = releaseweb.ReadKey(c.Paths)
	if !errors.Is(err, releaseweb.ErrNoKey) {
		return false, err
	}
	return true, releaseweb.RotateKey(c.Paths)
}

// Start creates a key if none exists, then starts the web detached, or reports
// the one already running.
func (c *ReleaseWebControl) Start(context.Context) (releaseweb.Status, error) {
	if _, err := c.EnsureKey(); err != nil {
		return releaseweb.Status{}, err
	}
	exe, err := c.Executable()
	if err != nil {
		return releaseweb.Status{}, fmt.Errorf("release web: find the ao binary: %w", err)
	}
	return releaseweb.Start(c.Paths, exe, []string{"release-web", "serve"})
}

// Stop ends the web if it runs and reports the status afterwards.
func (c *ReleaseWebControl) Stop(context.Context) (releaseweb.Status, error) {
	if err := releaseweb.Stop(c.Paths); err != nil {
		return releaseweb.Status{}, err
	}
	return releaseweb.ReadStatus(c.Paths), nil
}

// RotateKey replaces the key. A running web signs every session out.
func (c *ReleaseWebControl) RotateKey(context.Context) (releaseweb.Status, error) {
	if err := releaseweb.RotateKey(c.Paths); err != nil {
		return releaseweb.Status{}, err
	}
	return releaseweb.ReadStatus(c.Paths), nil
}

// CopyKey pipes the key into the clipboard command. The key goes from the file
// to the command's stdin and is never returned or logged.
func (c *ReleaseWebControl) CopyKey(context.Context) error {
	if c.Clipboard == nil {
		return releaseweb.ErrNoClipboard
	}
	if _, err := releaseweb.ReadKey(c.Paths); err != nil {
		return err
	}
	cmd := c.Clipboard()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("release web: clipboard: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("release web: clipboard: %w", err)
	}
	copyErr := releaseweb.CopyKey(c.Paths, stdin)
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("release web: clipboard command failed: %w", err)
	}
	return copyErr
}
