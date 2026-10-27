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

// ReleaseWebControl starts, stops and inspects the team web program.
type ReleaseWebControl struct {
	Paths      releaseweb.Paths
	Executable func() (string, error)
	Clipboard  func() *exec.Cmd
}

// NewReleaseWebControl returns the control for the release web beside dataDir.
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

// Status reports whether the web runs and the state of its key.
func (c *ReleaseWebControl) Status(context.Context) (releaseweb.Status, error) {
	return releaseweb.ReadStatus(c.Paths), nil
}

// Start starts the web detached, creating a key if none exists.
func (c *ReleaseWebControl) Start(ctx context.Context) (releaseweb.Status, error) {
	st, _, err := c.StartReportingKey(ctx)
	return st, err
}

// StartReportingKey is Start that also reports whether it created the key.
func (c *ReleaseWebControl) StartReportingKey(context.Context) (st releaseweb.Status, keyCreated bool, err error) {
	if _, err := releaseweb.ReadKey(c.Paths); errors.Is(err, releaseweb.ErrNoKey) {
		if err := releaseweb.RotateKey(c.Paths); err != nil {
			return releaseweb.Status{}, false, err
		}
		keyCreated = true
	} else if err != nil {
		return releaseweb.Status{}, false, err
	}
	exe, err := c.Executable()
	if err != nil {
		return releaseweb.Status{}, keyCreated, fmt.Errorf("release web: find the ao binary: %w", err)
	}
	st, err = releaseweb.Start(c.Paths, exe, []string{"release-web", "serve"})
	return st, keyCreated, err
}

// Stop ends the web if it runs and reports the status afterwards.
func (c *ReleaseWebControl) Stop(context.Context) (releaseweb.Status, error) {
	if err := releaseweb.Stop(c.Paths); err != nil {
		return releaseweb.Status{}, err
	}
	return releaseweb.ReadStatus(c.Paths), nil
}

// RotateKey replaces the key.
func (c *ReleaseWebControl) RotateKey(context.Context) (releaseweb.Status, error) {
	if err := releaseweb.RotateKey(c.Paths); err != nil {
		return releaseweb.Status{}, err
	}
	return releaseweb.ReadStatus(c.Paths), nil
}

// CopyKey pipes the key into the clipboard command.
func (c *ReleaseWebControl) CopyKey(context.Context) error {
	if _, err := releaseweb.ReadKey(c.Paths); err != nil {
		return err
	}
	if c.Clipboard == nil {
		return releaseweb.ErrNoClipboard
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
