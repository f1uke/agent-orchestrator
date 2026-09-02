// Package wikisettings holds the global Wiki configuration: where the user's
// personal note vault lives on disk, and which agent harness they last opened
// it with.
//
// It is deliberately global rather than per-project. The Wiki is a destination,
// not a project: it has no repo, no worktree, and no session, so there is no
// project row for the path to hang off.
//
// The vault path is empty by default and the Wiki surface stays hidden until it
// is set, so a checkout of this repository carries no one's personal path.
package wikisettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const fileName = "wiki-settings.json"

// Settings is the whole Wiki configuration.
type Settings struct {
	// VaultPath is the absolute path to the note vault. Empty means "not
	// configured", which hides the Wiki entirely.
	VaultPath string `json:"vaultPath"`
	// Harness is the agent the vault was last opened with, so the picker can
	// pre-select it and the next launch is one keystroke.
	Harness string `json:"harness,omitempty"`
}

// Default is the unconfigured state: no vault, no remembered agent.
func Default() Settings { return Settings{} }

// Store is the settings file, read once at construction and written atomically.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewStore opens (or defaults) the settings file under dir. A missing or
// unreadable file degrades to the unconfigured default rather than failing
// boot: an unset vault is a legitimate state, not an error.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("wikisettings: data dir is required")
	}
	s := &Store{path: filepath.Join(dir, fileName), cur: Default()}
	if b, err := os.ReadFile(s.path); err == nil {
		var loaded Settings
		if json.Unmarshal(b, &loaded) == nil {
			s.cur = normalize(loaded)
		}
	}
	return s, nil
}

// Get returns the current settings.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// VaultPath is the configured vault, or "" when the Wiki is not set up.
func (s *Store) VaultPath() string { return s.Get().VaultPath }

// Harness is the agent the vault was last opened with, or "" if never opened.
func (s *Store) Harness() string { return s.Get().Harness }

// Set replaces the whole value and persists it.
func (s *Store) Set(next Settings) error {
	next = normalize(next)
	b, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("wikisettings: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("wikisettings: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("wikisettings: rename: %w", err)
	}
	s.cur = next
	return nil
}

// SetHarness records the agent the vault was last opened with, leaving the
// vault path alone. Remembering the choice is a convenience, so a failed write
// is reported but the in-memory value still moves.
func (s *Store) SetHarness(harness string) error {
	cur := s.Get()
	cur.Harness = harness
	return s.Set(cur)
}

// normalize trims whitespace and expands a leading `~/`, so a hand-typed path
// resolves the same way the shell would.
func normalize(in Settings) Settings {
	in.VaultPath = ExpandHome(strings.TrimSpace(in.VaultPath))
	in.Harness = strings.TrimSpace(in.Harness)
	return in
}

// ExpandHome resolves a leading `~` or `~/` against the user's home directory.
// A path that does not start with `~` is returned unchanged, as is one that
// cannot be resolved (no home) — the caller's existence check reports that.
func ExpandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
