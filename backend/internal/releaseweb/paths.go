// Package releaseweb serves a read-only Release Train board to the team over a
// loopback port that the human exposes on the public internet with Tailscale
// Funnel. One shared secret key is the only gate, so every route here is
// written as internet-facing.
package releaseweb

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths names every file the release web keeps. All of them live in one
// directory under ~/.ao so the hard rule on app state holds.
type Paths struct {
	Dir string
}

// DefaultDir is $AO_RELEASE_WEB_DIR, else ~/.ao/release-web.
func DefaultDir() (string, error) {
	if d := os.Getenv("AO_RELEASE_WEB_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("release web: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".ao", "release-web"), nil
}

// Ensure creates Dir with 0700, and tightens it to 0700 when it already exists:
// key.json and access.log sit in it.
func (p Paths) Ensure() error {
	if p.Dir == "" {
		return fmt.Errorf("release web: empty dir")
	}
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return fmt.Errorf("release web: create %s: %w", p.Dir, err)
	}
	if err := os.Chmod(p.Dir, 0o700); err != nil {
		return fmt.Errorf("release web: chmod %s: %w", p.Dir, err)
	}
	return nil
}

// KeyFile holds the shared key, mode 0600.
func (p Paths) KeyFile() string { return filepath.Join(p.Dir, "key.json") }

// AccessLog is the append-only sign-in log, mode 0600.
func (p Paths) AccessLog() string { return filepath.Join(p.Dir, "access.log") }

// RunFile records the serving process while it listens.
func (p Paths) RunFile() string { return filepath.Join(p.Dir, "run.json") }

// StatusFile records the live session count while the server runs.
func (p Paths) StatusFile() string { return filepath.Join(p.Dir, "status.json") }

// WebLog receives a started server's stdout and stderr.
func (p Paths) WebLog() string { return filepath.Join(p.Dir, "web.log") }

// DBDir is the release web's own SQLite directory.
func (p Paths) DBDir() string { return filepath.Join(p.Dir, "db") }
