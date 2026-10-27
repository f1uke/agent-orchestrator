// Package releaseweb serves a read-only Release Train board over a loopback port that
// Tailscale Funnel exposes; one shared key is the only gate, so every route is internet-facing.
package releaseweb

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths names every file the release web keeps.
type Paths struct {
	Dir string
}

// DirFor is $AO_RELEASE_WEB_DIR, else the release-web directory beside the AO data dir.
func DirFor(dataDir string) string {
	if d := os.Getenv("AO_RELEASE_WEB_DIR"); d != "" {
		return d
	}
	return filepath.Join(filepath.Dir(dataDir), "release-web")
}

// Ensure creates Dir with mode 0700.
func (p Paths) Ensure() error {
	if p.Dir == "" {
		return fmt.Errorf("release web: empty dir")
	}
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return fmt.Errorf("release web: create %s: %w", p.Dir, err)
	}
	if err := os.Chmod(p.Dir, 0o700); err != nil { //nolint:gosec // G302: a directory needs its x bit; 0700 is owner-only
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
