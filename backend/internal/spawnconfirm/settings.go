// Package spawnconfirm holds the global "confirm before spawning a worker"
// setting, persisted as a small JSON file under the data dir (~/.ao). The
// session manager reads Get().Enabled when it assembles the orchestrator system
// prompt; the REST layer edits via Set(). Modeled on reclaimsettings.
package spawnconfirm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const fileName = "spawn-confirm-settings.json"

// Settings is the single spawn-confirm toggle.
type Settings struct {
	// Enabled gates the orchestrator on human confirmation before it runs
	// `ao spawn`. Default true (confirm).
	Enabled bool `json:"enabled"`
}

// Default is the confirm gate ON.
func Default() Settings { return Settings{Enabled: true} }

// Store is a mutex-guarded, file-backed Settings holder.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewStore loads dir/spawn-confirm-settings.json. A missing or corrupt file
// degrades to Default() (gate ON) rather than erroring, so the daemon always
// boots with the safe default.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("spawnconfirm: data dir is required")
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
		return fmt.Errorf("spawnconfirm: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("spawnconfirm: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("spawnconfirm: rename: %w", err)
	}
	s.cur = next
	return nil
}
