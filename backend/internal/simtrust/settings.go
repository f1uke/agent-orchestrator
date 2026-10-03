package simtrust

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const fileName = "sim-trust-settings.json"

// DefaultCAFiles is what a machine that never touched the setting trusts:
// wherever a known debugging proxy keeps the root CA it generated, if it is
// there. It is data, not a code path - the setting replaces it whole.
//
// Proxyman writes its CA here (the same certificate the Mac's own keychain
// trusts). Reading it is reading the proxy's file, not storing AO state, so the
// "all AO state lives under ~/.ao" rule is not in play.
var DefaultCAFiles = []string{
	"~/Library/Application Support/com.proxyman.NSProxy/app-data/proxyman-ca.pem",
}

// Settings is the global list of root-CA files AO makes every simulator trust.
// A project can replace it with its own list (domain.ProjectConfig.SimTrust).
type Settings struct {
	// CAFiles are PEM or DER root certificates, absolute or `~/`-relative. An
	// empty list trusts nothing; a missing file is skipped silently.
	CAFiles []string `json:"caFiles"`
}

// Default is the shipped setting: trust the known proxy CAs that exist.
func Default() Settings {
	return Settings{CAFiles: append([]string(nil), DefaultCAFiles...)}
}

// Validate rejects a path that can only be a mistake. The rules are the
// project override's, so the two spellings of this setting cannot drift.
func (s Settings) Validate() error { return domain.ValidateCAFiles("caFiles", s.CAFiles) }

// Store is a mutex-guarded, file-backed Settings holder, modeled on
// responselang.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewStore loads dir/sim-trust-settings.json. A missing file, a corrupt one, or
// one without a caFiles key degrades to Default() rather than erroring, so the
// daemon always boots - and a present `"caFiles": []` is still honoured as
// "trust nothing".
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("simtrust: data dir is required")
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
	return Settings{CAFiles: append([]string{}, s.cur.CAFiles...)}
}

// CAFiles is the current global list. Convenience for resolvers.
func (s *Store) CAFiles() []string { return s.Get().CAFiles }

// Set validates, persists (atomic write via temp+rename) and updates memory.
func (s *Store) Set(next Settings) error {
	if next.CAFiles == nil {
		next.CAFiles = []string{}
	}
	if err := next.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("simtrust: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("simtrust: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("simtrust: rename: %w", err)
	}
	s.cur = next
	return nil
}
