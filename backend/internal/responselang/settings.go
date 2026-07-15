// Package responselang holds the global default "human-facing response language"
// setting, persisted as a small JSON file under the data dir (~/.ao). The session
// manager and the review engine read Get().Language when they assemble a system
// prompt (resolving a per-project override over this default); the REST layer
// edits via Set(). Modeled on spawnconfirm.
package responselang

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
)

const fileName = "response-language-settings.json"

// Settings is the single global response-language value. Language is a free-form
// language name (e.g. "English", "Thai"); empty and "English" both mean "keep the
// current English behavior" (no directive injected).
type Settings struct {
	// Language is the global default human-facing response language.
	Language string `json:"language"`
}

// Default is the shipped English default, so every other user/project is
// unaffected until they opt in.
func Default() Settings { return Settings{Language: prompts.DefaultResponseLanguage} }

// Store is a mutex-guarded, file-backed Settings holder.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewStore loads dir/response-language-settings.json. A missing or corrupt file
// degrades to Default() (English) rather than erroring, so the daemon always
// boots with the safe default.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("responselang: data dir is required")
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

// Language returns the current global default language, trimmed. Convenience for
// the prompt-assembly getter closures.
func (s *Store) Language() string {
	return strings.TrimSpace(s.Get().Language)
}

// Set persists (atomic write via temp+rename) and updates memory. The language is
// trimmed; an empty value is stored as-is and resolves to English.
func (s *Store) Set(next Settings) error {
	next.Language = strings.TrimSpace(next.Language)
	b, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("responselang: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("responselang: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("responselang: rename: %w", err)
	}
	s.cur = next
	return nil
}
