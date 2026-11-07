// Package transportsettings persists the global default transports for new claude-code workers and orchestrators.
package transportsettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The file keeps the name it had when it held only the worker value, so a machine that set it keeps it.
const fileName = "worker-transport-settings.json"

// Settings are the global transport values.
type Settings struct {
	WorkerTransport       domain.Transport `json:"workerTransport"`
	OrchestratorTransport domain.Transport `json:"orchestratorTransport"`
}

// Default is the setting of a machine that never changed it.
func Default() Settings {
	return Settings{WorkerTransport: domain.DefaultClaudeCodeTransport, OrchestratorTransport: domain.DefaultClaudeCodeTransport}
}

// Validate refuses anything but a named transport.
func (s Settings) Validate() error {
	if err := validateNamed("workerTransport", s.WorkerTransport); err != nil {
		return err
	}
	return validateNamed("orchestratorTransport", s.OrchestratorTransport)
}

func validateNamed(field string, t domain.Transport) error {
	if t == domain.TransportUnresolved {
		return fmt.Errorf("%s is required: terminal or chat", field)
	}
	_, err := domain.ParseTransport(string(t))
	return err
}

// Store is a mutex-guarded, file-backed Settings holder.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewStore loads the settings file in dir, falling back to Default(). A file
// that names only some values keeps the default for the rest.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("transportsettings: data dir is required")
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

// WorkerTransport returns the global transport for new workers.
func (s *Store) WorkerTransport() domain.Transport { return s.Get().WorkerTransport }

// OrchestratorTransport returns the global transport for new orchestrators.
func (s *Store) OrchestratorTransport() domain.Transport { return s.Get().OrchestratorTransport }

// SetWorker persists the worker transport and leaves the orchestrator one as it is.
func (s *Store) SetWorker(t domain.Transport) error {
	return s.update(func(cur *Settings) { cur.WorkerTransport = t })
}

// SetOrchestrator persists the orchestrator transport and leaves the worker one as it is.
func (s *Store) SetOrchestrator(t domain.Transport) error {
	return s.update(func(cur *Settings) { cur.OrchestratorTransport = t })
}

func (s *Store) update(change func(*Settings)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cur
	change(&next)
	if err := next.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("transportsettings: marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("transportsettings: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("transportsettings: rename: %w", err)
	}
	s.cur = next
	return nil
}
