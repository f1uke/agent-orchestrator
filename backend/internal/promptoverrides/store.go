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

// Overrides maps a prompt kind to its custom global base and each message
// template name to its custom text. A missing key means the built-in default
// applies.
type Overrides struct {
	Base      map[prompts.Kind]string `json:"base,omitempty"`
	Templates map[string]string       `json:"templates,omitempty"`
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
	s := &Store{path: filepath.Join(dir, fileName), cur: Overrides{
		Base:      map[prompts.Kind]string{},
		Templates: map[string]string{},
	}}
	if b, err := os.ReadFile(s.path); err == nil {
		var loaded Overrides
		if json.Unmarshal(b, &loaded) == nil {
			if loaded.Base != nil {
				s.cur.Base = loaded.Base
			}
			if loaded.Templates != nil {
				s.cur.Templates = loaded.Templates
			}
		}
	}
	return s, nil
}

// Get returns a copy of the current overrides; callers cannot mutate the store.
func (s *Store) Get() Overrides {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Overrides{
		Base:      make(map[prompts.Kind]string, len(s.cur.Base)),
		Templates: make(map[string]string, len(s.cur.Templates)),
	}
	for k, v := range s.cur.Base {
		out.Base[k] = v
	}
	for k, v := range s.cur.Templates {
		out.Templates[k] = v
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
	prev := s.cur.Base[k]
	had := s.cur.Base != nil
	if s.cur.Base == nil {
		s.cur.Base = map[prompts.Kind]string{}
	}
	s.cur.Base[k] = text
	if err := s.persistLocked(); err != nil {
		if had {
			s.cur.Base[k] = prev
		} else {
			s.cur.Base = nil
		}
		return err
	}
	return nil
}

// ClearBase removes a kind's override, restoring the built-in default.
func (s *Store) ClearBase(k prompts.Kind) error {
	if !k.Valid() {
		return fmt.Errorf("promptoverrides: unknown kind %q", k)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, existed := s.cur.Base[k]
	delete(s.cur.Base, k)
	if err := s.persistLocked(); err != nil {
		if existed {
			s.cur.Base[k] = prev
		}
		return err
	}
	return nil
}

// GetTemplate returns the custom override for a message template name and
// whether one exists. Absent ⇒ ("", false) ⇒ caller uses the built-in default.
func (s *Store) GetTemplate(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.cur.Templates[name]
	return v, ok
}

// SetTemplate stores a custom message-template override.
func (s *Store) SetTemplate(name, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, existed := s.cur.Templates[name]
	if s.cur.Templates == nil {
		s.cur.Templates = map[string]string{}
	}
	s.cur.Templates[name] = text
	if err := s.persistLocked(); err != nil {
		if existed {
			s.cur.Templates[name] = prev
		} else {
			delete(s.cur.Templates, name)
		}
		return err
	}
	return nil
}

// ClearTemplate removes a message-template override, restoring the default.
func (s *Store) ClearTemplate(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, existed := s.cur.Templates[name]
	delete(s.cur.Templates, name)
	if err := s.persistLocked(); err != nil {
		if existed {
			s.cur.Templates[name] = prev
		}
		return err
	}
	return nil
}

// persistLocked writes s.cur to disk atomically (temp+rename). Callers hold
// s.mu.
func (s *Store) persistLocked() error {
	b, err := json.Marshal(s.cur)
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
