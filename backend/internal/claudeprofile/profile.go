// Package claudeprofile is the registry of Claude profiles: named Claude Code
// settings files a claude-code session launches with (`claude --settings
// <file>`), layered on top of ~/.claude/settings.json. Two are built in; the
// rest are the user's, persisted as names and paths only under the data dir.
package claudeprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/fsatomic"
)

const fileName = "claude-profiles.json"

// The built-in profile names.
const (
	SubscriptionName = "Subscription"
	OmniRouteName    = "OmniRoute"
)

// Profile is one named Claude Code settings file. An empty SettingsFile means
// the session launches with no --settings at all.
type Profile struct {
	Name         string `json:"name"`
	SettingsFile string `json:"settingsFile"`
	Builtin      bool   `json:"builtin"`
}

// Subscription is the default profile: plain `claude`, no settings layered on.
func Subscription() Profile {
	return Profile{Name: SubscriptionName, Builtin: true}
}

func builtins() []Profile {
	return []Profile{
		Subscription(),
		{Name: OmniRouteName, SettingsFile: "~/.claude/settings-omniroute.json", Builtin: true},
	}
}

var (
	// ErrUnknownProfile is a profile name the registry does not hold.
	ErrUnknownProfile = errors.New("claudeprofile: unknown profile")
	// ErrSettingsFile is a profile whose settings file Claude Code cannot use.
	ErrSettingsFile = errors.New("claudeprofile: unusable settings file")
)

// UnknownProfileError names the profile asked for and every profile there is.
type UnknownProfileError struct {
	Name  string
	Known []string
}

func (e *UnknownProfileError) Error() string {
	return fmt.Sprintf("unknown Claude profile %q (known: %s)", e.Name, strings.Join(e.Known, ", "))
}

func (e *UnknownProfileError) Unwrap() error { return ErrUnknownProfile }

// Store holds the user profiles, file-backed under the data dir.
type Store struct {
	path string
	mu   sync.RWMutex
	user []Profile
}

// NewStore loads dir/claude-profiles.json. A missing or corrupt file leaves
// only the built-ins, so the daemon always boots.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("claudeprofile: data dir is required")
	}
	s := &Store{path: filepath.Join(dir, fileName)}
	if b, err := os.ReadFile(s.path); err == nil {
		var loaded []Profile
		if json.Unmarshal(b, &loaded) == nil && validateUser(loaded) == nil {
			s.user = asUser(loaded)
		}
	}
	return s, nil
}

// Builtins is a registry holding only the built-in profiles, for callers wired
// without a data dir.
func Builtins() *Store { return &Store{} }

// List returns the built-ins first, then the user profiles.
func (s *Store) List() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append(builtins(), s.user...)
}

// Resolve finds a profile by name, ignoring case, and returns it under its
// canonical name. An empty name is Subscription.
func (s *Store) Resolve(name string) (Profile, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Subscription(), true
	}
	for _, p := range s.List() {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Profile{}, false
}

// Lookup is Resolve with an UnknownProfileError for a name it does not hold.
func (s *Store) Lookup(name string) (Profile, error) {
	if p, ok := s.Resolve(name); ok {
		return p, nil
	}
	all := s.List()
	known := make([]string, 0, len(all))
	for _, p := range all {
		known = append(known, p.Name)
	}
	return Profile{}, &UnknownProfileError{Name: strings.TrimSpace(name), Known: known}
}

// SetUser validates and replaces the user profiles, persisting them atomically.
// A rejected list changes nothing.
func (s *Store) SetUser(profiles []Profile) error {
	if err := validateUser(profiles); err != nil {
		return err
	}
	if s.path == "" {
		return errors.New("claudeprofile: this registry has no data dir to persist user profiles in")
	}
	next := asUser(profiles)
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("claudeprofile: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fsatomic.WriteFile(s.path, b, 0o600); err != nil {
		return fmt.Errorf("claudeprofile: write: %w", err)
	}
	s.user = next
	return nil
}

func asUser(profiles []Profile) []Profile {
	out := make([]Profile, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, Profile{Name: strings.TrimSpace(p.Name), SettingsFile: strings.TrimSpace(p.SettingsFile)})
	}
	return out
}

func validateUser(profiles []Profile) error {
	seen := map[string]bool{}
	for _, b := range builtins() {
		seen[strings.ToLower(b.Name)] = true
	}
	for _, p := range profiles {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			return errors.New("a Claude profile needs a name")
		}
		if seen[strings.ToLower(name)] {
			return fmt.Errorf("a Claude profile named %q is already defined", name)
		}
		seen[strings.ToLower(name)] = true
		file := strings.TrimSpace(p.SettingsFile)
		if file == "" {
			return fmt.Errorf("the Claude profile %q needs a settings file", name)
		}
		if !filepath.IsAbs(file) && !strings.HasPrefix(file, "~/") {
			return fmt.Errorf("the settings file %q of Claude profile %q must be absolute or start with ~/", file, name)
		}
	}
	return nil
}

// SettingsPath is the file a session on this profile passes to `claude
// --settings`, with ~ expanded: "" for a profile with no file. Claude Code exits
// on a missing file and silently ignores one that is not valid JSON, so both are
// refused here, before anything is launched.
func SettingsPath(p Profile) (string, error) {
	file := strings.TrimSpace(p.SettingsFile)
	if file == "" {
		return "", nil
	}
	if rest, ok := strings.CutPrefix(file, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", &SettingsFileError{Profile: p.Name, File: file, Problem: "cannot be resolved: " + err.Error()}
		}
		file = filepath.Join(home, rest)
	}
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return "", &SettingsFileError{Profile: p.Name, File: file, Problem: "does not exist"}
	}
	if err != nil {
		return "", &SettingsFileError{Profile: p.Name, File: file, Problem: "cannot be read: " + err.Error()}
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil || obj == nil {
		return "", &SettingsFileError{Profile: p.Name, File: file, Problem: "is not a JSON object"}
	}
	return file, nil
}

// SettingsFileError is a profile whose settings file Claude Code cannot use.
type SettingsFileError struct {
	Profile string
	File    string
	Problem string
}

func (e *SettingsFileError) Error() string {
	return fmt.Sprintf("the settings file %s of Claude profile %q %s", e.File, e.Profile, e.Problem)
}

func (e *SettingsFileError) Unwrap() error { return ErrSettingsFile }
