// Package workertransport persists the global default transport for new claude-code workers.
package workertransport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const fileName = "worker-transport-settings.json"

// Settings is the global worker-transport value.
type Settings struct {
	WorkerTransport domain.Transport `json:"workerTransport"`
}

// Default is the setting of a machine that never changed it.
func Default() Settings { return Settings{WorkerTransport: domain.DefaultWorkerTransport} }

// Validate refuses anything but a named transport.
func (s Settings) Validate() error {
	if s.WorkerTransport == "" {
		return errors.New("workerTransport is required: terminal or chat")
	}
	_, err := domain.ParseTransport(string(s.WorkerTransport))
	return err
}

// Store is a mutex-guarded, file-backed Settings holder.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewStore loads the settings file in dir, falling back to Default().
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("workertransport: data dir is required")
	}
	s := &Store{path: filepath.Join(dir, fileName), cur: Default()}
	if b, err := os.ReadFile(s.path); err == nil {
		var loaded Settings
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

// Transport returns the current global transport.
func (s *Store) Transport() domain.Transport { return s.Get().WorkerTransport }

// Set validates and persists the settings.
func (s *Store) Set(next Settings) error {
	if err := next.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("workertransport: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("workertransport: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("workertransport: rename: %w", err)
	}
	s.cur = next
	return nil
}
