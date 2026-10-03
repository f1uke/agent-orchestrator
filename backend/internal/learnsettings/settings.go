// Package learnsettings holds the user-editable knobs of learning's model
// stages, persisted as a small JSON file under the data dir (~/.ao). Which
// projects learn at all is per project (ProjectConfig.LearnFromSessions); this
// is what the model calls may cost and which model makes them.
package learnsettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const fileName = "learning-settings.json"

// Defaults, decided with the human (2026-10-03): Sonnet 5.5 at low effort
// collected the same lessons as Haiku 23x faster and scoped them better in the
// spike; a measured batch costs about $0.003 per human turn, so $2 a day covers
// roughly ten times this machine's measured daily volume.
const (
	DefaultCollectModel  = "claude-sonnet-5-5"
	DefaultCollectEffort = "low"
	// DefaultRulesModel atomizes the standing-rules corpus: rewriting a file
	// into statements takes no judgment about what is a lesson, so it stays on
	// the cheapest model (decision 7).
	DefaultRulesModel  = "claude-haiku-4-5-20251001"
	DefaultDailyBudget = 2.0
	// MaxDailyBudget caps a typo.
	MaxDailyBudget = 100.0
)

// Settings are learning's model knobs.
type Settings struct {
	CollectModel  string `json:"collectModel"`
	CollectEffort string `json:"collectEffort"`
	// RulesModel splits the rules agents are already told into statements.
	RulesModel string `json:"rulesModel"`
	// DailyBudgetUSD stops the background model runs (collect and rules) for
	// the rest of the local day once the day's runs have cost this much. Zero
	// pauses them.
	DailyBudgetUSD float64 `json:"dailyBudgetUSD"`
}

// Default returns the out-of-the-box settings.
func Default() Settings {
	return Settings{CollectModel: DefaultCollectModel, CollectEffort: DefaultCollectEffort, RulesModel: DefaultRulesModel, DailyBudgetUSD: DefaultDailyBudget}
}

var validEffort = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// Validate refuses settings a model call cannot run with.
func (s Settings) Validate() error {
	if s.CollectModel == "" {
		return errors.New("learnsettings: collectModel is required")
	}
	if s.RulesModel == "" {
		return errors.New("learnsettings: rulesModel is required")
	}
	if !validEffort[s.CollectEffort] {
		return fmt.Errorf("learnsettings: collectEffort must be low, medium, high, xhigh or max, got %q", s.CollectEffort)
	}
	if s.DailyBudgetUSD < 0 || s.DailyBudgetUSD > MaxDailyBudget {
		return fmt.Errorf("learnsettings: dailyBudgetUSD must be between 0 and %.0f, got %v", MaxDailyBudget, s.DailyBudgetUSD)
	}
	return nil
}

// Store is a mutex-guarded, file-backed Settings holder.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewStore loads dir/learning-settings.json. A missing or invalid file degrades
// to Default() so the daemon always boots.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("learnsettings: data dir is required")
	}
	s := &Store{path: filepath.Join(dir, fileName), cur: Default()}
	if b, err := os.ReadFile(s.path); err == nil {
		loaded := Default()
		if json.Unmarshal(b, &loaded) == nil && loaded.Validate() == nil {
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

// Set validates, persists (temp + rename) and applies next.
func (s *Store) Set(next Settings) error {
	if err := next.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("learnsettings: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("learnsettings: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("learnsettings: rename: %w", err)
	}
	s.cur = next
	return nil
}
