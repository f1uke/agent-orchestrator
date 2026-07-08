// Package promptoverrides holds the user-editable global base override for each
// system-prompt kind, persisted as a small JSON file under the data dir (~/.ao).
// Absent key ⇒ use the built-in default from package prompts. Modeled on
// spawnconfirm. The session manager and review engine read Get(); the REST layer
// edits via SetBase/ClearBase.
package promptoverrides

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
)

const fileName = "system-prompt-overrides.json"

// Overrides maps a prompt kind to its custom global base. A missing key means
// the built-in default applies.
type Overrides struct {
	Base map[prompts.Kind]string `json:"base,omitempty"`
}

// Store is a mutex-guarded, file-backed Overrides holder.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Overrides
}

// NewStore loads dir/system-prompt-overrides.json. A missing or corrupt file
// degrades to no overrides (built-in defaults) so the daemon always boots.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("promptoverrides: data dir is required")
	}
	s := &Store{path: filepath.Join(dir, fileName), cur: Overrides{Base: map[prompts.Kind]string{}}}
	if b, err := os.ReadFile(s.path); err == nil {
		var loaded Overrides
		if json.Unmarshal(b, &loaded) == nil && loaded.Base != nil {
			s.cur = loaded
		}
	}
	return s, nil
}

// Get returns a copy of the current overrides; callers cannot mutate the store.
func (s *Store) Get() Overrides {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Overrides{Base: make(map[prompts.Kind]string, len(s.cur.Base))}
	for k, v := range s.cur.Base {
		out.Base[k] = v
	}
	return out
}

// SetBase stores a custom global base for a kind.
func (s *Store) SetBase(k prompts.Kind, text string) error {
	if !k.Valid() {
		return fmt.Errorf("promptoverrides: unknown kind %q", k)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := s.copyBaseLocked()
	candidate[k] = text
	if err := s.persistLocked(candidate); err != nil {
		return err
	}
	s.cur.Base = candidate
	return nil
}

// ClearBase removes a kind's override, restoring the built-in default.
func (s *Store) ClearBase(k prompts.Kind) error {
	if !k.Valid() {
		return fmt.Errorf("promptoverrides: unknown kind %q", k)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := s.copyBaseLocked()
	delete(candidate, k)
	if err := s.persistLocked(candidate); err != nil {
		return err
	}
	s.cur.Base = candidate
	return nil
}

// copyBaseLocked returns a mutable copy of s.cur.Base for building a candidate
// state. Callers hold s.mu.
func (s *Store) copyBaseLocked() map[prompts.Kind]string {
	out := make(map[prompts.Kind]string, len(s.cur.Base))
	for k, v := range s.cur.Base {
		out[k] = v
	}
	return out
}

// persistLocked writes the given candidate base map to disk atomically
// (temp+rename), without mutating s.cur. Callers hold s.mu and, on success,
// are responsible for assigning the candidate to s.cur.
func (s *Store) persistLocked(base map[prompts.Kind]string) error {
	b, err := json.Marshal(Overrides{Base: base})
	if err != nil {
		return fmt.Errorf("promptoverrides: marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("promptoverrides: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("promptoverrides: rename: %w", err)
	}
	return nil
}
