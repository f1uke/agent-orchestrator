package simpower

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const settingsFileName = "sim-boot-settings.json"

// DefaultMaxBooted is the boot cap on a machine nobody has configured: how
// many simulators may be up or coming up at once, machine-wide.
//
// The number rests on one measurement and one decision. The measurement: three
// booted simulators once ran the machine this was built for out of memory, at
// the per-device size of the time. Each is a virtual machine of several GB that
// outlives the session that started it, and a lease serialises DRIVING one
// device - it does nothing whatever about how many exist. The decision: the
// cap used to be 2, and now that every iOS worker gets its own clone the human
// chose 4. A cap is no longer the only defence either: when it is reached, `ao
// sim boot` shuts down an idle AO clone to make room (see Settings).
const DefaultMaxBooted = 4

// Settings is the machine-wide boot cap. It is global rather than per project
// because the memory it guards is the machine's: two projects' simulators
// share the same RAM.
type Settings struct {
	// MaxBooted is how many simulators may be up or coming up at once. A boot
	// past it is refused - except from `ao sim boot`, which first shuts down
	// the least recently booted AO clone nobody holds a lease on.
	MaxBooted int `json:"maxBooted"`
}

// DefaultSettings is the cap on a machine that never touched the setting.
func DefaultSettings() Settings { return Settings{MaxBooted: DefaultMaxBooted} }

// Validate rejects a cap that would refuse every boot. Below one, no simulator
// could ever come up, and `ao sim boot` exists because a machine with none
// deadlocks qa.
func (s Settings) Validate() error {
	if s.MaxBooted < 1 {
		return fmt.Errorf("simpower: maxBooted must be at least 1, got %d", s.MaxBooted)
	}
	return nil
}

// SettingsStore is a mutex-guarded, file-backed Settings holder, modeled on
// the other settings stores under the data dir.
type SettingsStore struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewSettingsStore loads dir/sim-boot-settings.json. A missing, corrupt or
// invalid file degrades to DefaultSettings rather than erroring, so the daemon
// always boots.
func NewSettingsStore(dir string) (*SettingsStore, error) {
	if dir == "" {
		return nil, errors.New("simpower: data dir is required")
	}
	s := &SettingsStore{path: filepath.Join(dir, settingsFileName), cur: DefaultSettings()}
	if b, err := os.ReadFile(s.path); err == nil {
		loaded := DefaultSettings()
		if json.Unmarshal(b, &loaded) == nil && loaded.Validate() == nil {
			s.cur = loaded
		}
	}
	return s, nil
}

// Get returns the current settings.
func (s *SettingsStore) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Set validates, persists (atomic write via temp+rename) and updates memory.
func (s *SettingsStore) Set(next Settings) error {
	if err := next.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("simpower: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("simpower: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("simpower: rename: %w", err)
	}
	s.cur = next
	return nil
}
