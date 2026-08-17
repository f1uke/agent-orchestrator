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

// DefaultGraceMinutes is 24 hours.
//
// Auto-reclaim deletes silently, so the grace is the whole of the user's
// "wait, I still needed that" window. 24h is chosen because it is the smallest
// value that survives an overnight gap: a PR merged at 18:00 is still on disk
// when its author sits down the next morning, which a minutes-scale grace is
// not. It is not larger because reclaim keeps the branch — the cost of being
// wrong is a re-checkout (plus, for a native app, a rebuild), not lost work,
// so the window only has to outlast human attention, not human memory.
const DefaultGraceMinutes = 24 * 60

// DefaultArtifactPatterns are untracked paths treated as REGENERABLE BUILD
// OUTPUT rather than as human work, so their presence alone does not make a
// finished worktree un-reclaimable.
//
// Every entry here must be something a build reproduces from committed sources.
// Deliberately absent: bare `build`, `dist`, `out`, `target` — those names are
// also used for hand-written content, and a false positive here deletes work.
// A pattern matches an untracked entry when it equals one of the entry's path
// segments, or when path.Match accepts the whole relative path.
var DefaultArtifactPatterns = []string{
	"derivedDataPath",
	"DerivedData",
	"*.xcresult",
	"xcov_report",
	"Pods",
	"Carthage",
	".build",
	".swiftpm",
	"node_modules",
	".gradle",
	".venv",
	"__pycache__",
}

// Settings are the knobs behind auto-reclaim.
type Settings struct {
	Enabled      bool `json:"enabled"`
	GraceMinutes int  `json:"graceMinutes"`
	// ArtifactsEnabled lets untracked regenerable build output be cleared out of
	// the way of an otherwise-clean finished worktree. With it off, a single
	// `derivedDataPath/` pins the whole worktree on disk forever.
	ArtifactsEnabled bool `json:"artifactsEnabled"`
	// ArtifactPatterns overrides DefaultArtifactPatterns when non-empty.
	ArtifactPatterns []string `json:"artifactPatterns,omitempty"`
}

// Default is auto-reclaim ON with a 24-hour grace and artefact clearing ON.
func Default() Settings {
	return Settings{
		Enabled:          true,
		GraceMinutes:     DefaultGraceMinutes,
		ArtifactsEnabled: true,
	}
}

// Patterns returns the effective artefact patterns: the user's override when
// set, otherwise the defaults. Empty when artefact clearing is off, so callers
// cannot accidentally classify anything as regenerable.
func (s Settings) Patterns() []string {
	if !s.ArtifactsEnabled {
		return nil
	}
	if len(s.ArtifactPatterns) > 0 {
		return s.ArtifactPatterns
	}
	return DefaultArtifactPatterns
}

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
		// Unmarshal ONTO a Default() value, not a zero value: a settings file
		// written before a knob existed omits its key, and decoding into a zero
		// struct would silently read that absence as "off". Keys the file does
		// carry still win, including an explicit false.
		loaded := Default()
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
