// Package reclaimsettings holds the user-editable auto-reclaim settings,
// persisted as a small JSON file under the data dir (~/.ao). The daemon's
// reclaim loop reads Get() each tick; the REST layer edits via Set().
package reclaimsettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const fileName = "reclaim-settings.json"

// Settings are the two knobs behind auto-reclaim.
type Settings struct {
	Enabled      bool `json:"enabled"`
	GraceMinutes int  `json:"graceMinutes"`
}

// Default is auto-reclaim ON with a 15-minute grace.
func Default() Settings { return Settings{Enabled: true, GraceMinutes: 15} }

// Store is a mutex-guarded, file-backed Settings holder.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewStore loads dir/reclaim-settings.json. A missing or corrupt file degrades
// to Default() rather than erroring, so the daemon always boots.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("reclaimsettings: data dir is required")
	}
	s := &Store{path: filepath.Join(dir, fileName), cur: Default()}
	if b, err := os.ReadFile(s.path); err == nil {
		var loaded Settings
		if json.Unmarshal(b, &loaded) == nil && loaded.GraceMinutes >= 0 {
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

// Set validates, persists (atomic write via temp+rename), and updates memory.
func (s *Store) Set(next Settings) error {
	if next.GraceMinutes < 0 {
		return fmt.Errorf("reclaimsettings: graceMinutes must be >= 0, got %d", next.GraceMinutes)
	}
	b, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("reclaimsettings: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("reclaimsettings: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("reclaimsettings: rename: %w", err)
	}
	s.cur = next
	return nil
}
