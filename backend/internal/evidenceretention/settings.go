// Package evidenceretention holds the user-editable age-based retention policy
// for smoke-test evidence blobs, persisted as a small JSON file under the data
// dir (~/.ao). The daemon's retention sweep reads Get() each tick and purges
// evidence whose DB created_at is older than the (clamped) TTL; the REST layer
// edits via Set(). Disabling (Enabled=false or MaxAgeDays<=0) keeps evidence
// forever — retention is purely age-based and never fires during normal
// cleanup/suspend.
package evidenceretention

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const fileName = "evidence-retention-settings.json"

const (
	// DefaultRetentionDays is the out-of-the-box TTL: evidence older than 30
	// days is purged.
	DefaultRetentionDays = 30
	// MinRetentionDays is the floor a positive TTL is clamped UP to. It guards
	// against a fat-fingered tiny value (e.g. a stray "0.something" rounded to a
	// sub-day) silently wiping evidence the user just captured: the sweep never
	// purges anything younger than a full day.
	MinRetentionDays = 1
	// MaxRetentionDays caps the TTL (~10 years) so the stored value stays sane.
	MaxRetentionDays = 3650
)

// Settings are the two knobs behind evidence retention: whether the age sweep
// runs at all, and how many days an evidence blob is kept from its created_at.
type Settings struct {
	Enabled    bool `json:"enabled"`
	MaxAgeDays int  `json:"maxAgeDays"`
}

// Default is retention ON with a 30-day TTL.
func Default() Settings { return Settings{Enabled: true, MaxAgeDays: DefaultRetentionDays} }

// ClampDays clamps a positive day count into [MinRetentionDays, MaxRetentionDays].
// A non-positive count is returned unchanged so callers can treat it as
// "disabled" rather than silently promoting it to the floor.
func ClampDays(days int) int {
	if days <= 0 {
		return days
	}
	if days < MinRetentionDays {
		return MinRetentionDays
	}
	if days > MaxRetentionDays {
		return MaxRetentionDays
	}
	return days
}

// Cutoff returns the timestamp before which evidence is considered expired, and
// ok=false when retention is disabled (Enabled=false or a non-positive TTL =
// keep forever). The day count is clamped into [Min, Max] first, so a
// misconfigured tiny TTL can never purge evidence younger than a day.
func (s Settings) Cutoff(now time.Time) (time.Time, bool) {
	if !s.Enabled || s.MaxAgeDays <= 0 {
		return time.Time{}, false
	}
	days := ClampDays(s.MaxAgeDays)
	return now.Add(-time.Duration(days) * 24 * time.Hour), true
}

// Store is a mutex-guarded, file-backed Settings holder.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewStore loads dir/evidence-retention-settings.json. A missing or corrupt
// file degrades to Default() rather than erroring, so the daemon always boots.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("evidenceretention: data dir is required")
	}
	s := &Store{path: filepath.Join(dir, fileName), cur: Default()}
	if b, err := os.ReadFile(s.path); err == nil {
		var loaded Settings
		if json.Unmarshal(b, &loaded) == nil && loaded.MaxAgeDays >= 0 {
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
	if next.MaxAgeDays < 0 {
		return fmt.Errorf("evidenceretention: maxAgeDays must be >= 0, got %d", next.MaxAgeDays)
	}
	if next.MaxAgeDays > MaxRetentionDays {
		return fmt.Errorf("evidenceretention: maxAgeDays must be <= %d, got %d", MaxRetentionDays, next.MaxAgeDays)
	}
	b, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("evidenceretention: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("evidenceretention: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("evidenceretention: rename: %w", err)
	}
	s.cur = next
	return nil
}
