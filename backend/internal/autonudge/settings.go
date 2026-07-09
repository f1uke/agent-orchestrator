// Package autonudge holds the global "auto-nudge the worker when a PR has
// unresolved review comments" setting, persisted as a small JSON file under
// the data dir (~/.ao). The Lifecycle Manager reads Get().Enabled when it
// decides whether to nudge the worker on unresolved review threads; the REST
// layer edits via Set(). Modeled on reclaimsettings.
package autonudge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const fileName = "auto-nudge-settings.json"

// Settings is the single auto-nudge toggle.
type Settings struct {
	// Enabled gates whether the Lifecycle Manager nudges the worker when a PR
	// has unresolved review comments. Default false (off).
	Enabled bool `json:"enabled"`
}

// Default is the auto-nudge gate OFF.
func Default() Settings { return Settings{Enabled: false} }

// Store is a mutex-guarded, file-backed Settings holder.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewStore loads dir/auto-nudge-settings.json. A missing or corrupt file
// degrades to Default() (gate OFF) rather than erroring, so the daemon always
// boots with the safe default.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("autonudge: data dir is required")
	}
	s := &Store{path: filepath.Join(dir, fileName), cur: Default()}
	if b, err := os.ReadFile(s.path); err == nil {
		var loaded Settings
		if json.Unmarshal(b, &loaded) == nil {
			s.cur = loaded
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

// Set persists (atomic write via temp+rename) and updates memory.
func (s *Store) Set(next Settings) error {
	b, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("autonudge: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("autonudge: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("autonudge: rename: %w", err)
	}
	s.cur = next
	return nil
}
