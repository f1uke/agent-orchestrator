// Package wikisettings holds the global Wiki configuration: where the user's
// personal note vault lives on disk, and which agent harness they last opened
// it with.
//
// It is deliberately global rather than per-project. The Wiki is a destination,
// not a project: it has no repo, no worktree, and no session, so there is no
// project row for the path to hang off.
//
// The vault path is empty by default and the Wiki surface stays hidden until it
// is set, so a checkout of this repository carries no one's personal path.
package wikisettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const fileName = "wiki-settings.json"

// Settings is the whole Wiki configuration.
type Settings struct {
	// VaultPath is the absolute path to the note vault. Empty means "not
	// configured", which hides the Wiki entirely.
	VaultPath string `json:"vaultPath"`
	// Harness is the agent the vault was last opened with, so the picker can
	// pre-select it and the next launch is one keystroke.
	Harness string `json:"harness,omitempty"`
	// Tasks configures the Tasks tab: which corners of the vault hold task
	// rows, and how to read ownership out of them.
	Tasks TaskSettings `json:"tasks,omitzero"`
}

// TaskSettings says where the Tasks tab looks and what it counts as "mine".
//
// Every field is a value the USER supplies. This package ships no folder name,
// no section name and no person: a vault's task convention belongs to whoever
// writes the vault, and encoding one here would make the tab work for exactly
// one person's notes.
type TaskSettings struct {
	// Folders are the vault-relative subtrees scanned for task rows. Empty
	// means "not configured", and an unconfigured tab scans NOTHING — a
	// whole-vault default would drag in every checkbox that was never a task.
	//
	// A LIST rather than one folder because a vault that separates ongoing
	// areas from delivered projects keeps live task rows in both, while the
	// folders it wants left alone (an archive, a raw capture inbox) sit
	// alongside them. One subtree would force a choice between reading half
	// the work and reading the noise.
	Folders []string `json:"folders,omitempty"`
	// Sections narrows the scan to rows under these "## " headings. Empty means
	// every section.
	Sections []string `json:"sections,omitempty"`
	// Cutoff is a YYYY-MM-DD date. Rows older than it are HIDDEN by the tab —
	// never modified, never deleted. Empty means no cutoff.
	Cutoff string `json:"cutoff,omitempty"`
	// OwnerAliases are the owner tokens that mean "me", so the tab can offer a
	// mine/others filter without this repo knowing anyone's name.
	OwnerAliases []string `json:"ownerAliases,omitempty"`
	// RequireCreated narrows the tab to rows that carry a `created:` date of
	// their own, and makes that date the only one the cutoff judges by.
	//
	// It is OFF by default, and it must stay that way: turning it on hides
	// every row that has not been tagged yet, which in a vault part-way
	// through adopting `created:` is nearly all of them. That is a legitimate
	// thing to ask for — a reader who tags as they capture wants the untagged
	// backlog out of the way — but it is never something to assume, so a
	// settings file written before this field existed reads as false.
	//
	// Like Cutoff, it HIDES and never modifies: the rows stay in the notes.
	RequireCreated bool `json:"requireCreated,omitempty"`
}

// Default is the unconfigured state: no vault, no remembered agent, no tasks.
func Default() Settings { return Settings{} }

// Store is the settings file, read once at construction and written atomically.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewStore opens (or defaults) the settings file under dir. A missing or
// unreadable file degrades to the unconfigured default rather than failing
// boot: an unset vault is a legitimate state, not an error.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("wikisettings: data dir is required")
	}
	s := &Store{path: filepath.Join(dir, fileName), cur: Default()}
	if b, err := os.ReadFile(s.path); err == nil {
		var loaded Settings
		if json.Unmarshal(b, &loaded) == nil {
			s.cur = normalize(loaded)
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

// VaultPath is the configured vault, or "" when the Wiki is not set up.
func (s *Store) VaultPath() string { return s.Get().VaultPath }

// Harness is the agent the vault was last opened with, or "" if never opened.
func (s *Store) Harness() string { return s.Get().Harness }

// Tasks is the Tasks tab's configuration, zero-valued when it was never set up.
func (s *Store) Tasks() TaskSettings { return s.Get().Tasks }

// SetTasks replaces the Tasks configuration, leaving the vault path and the
// remembered harness alone — the same rule SetHarness follows, for the same
// reason: these three are written by three different surfaces and none of them
// may blank the others.
func (s *Store) SetTasks(next TaskSettings) error {
	cur := s.Get()
	cur.Tasks = next
	return s.Set(cur)
}

// Set replaces the whole value and persists it.
func (s *Store) Set(next Settings) error {
	next = normalize(next)
	b, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("wikisettings: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("wikisettings: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("wikisettings: rename: %w", err)
	}
	s.cur = next
	return nil
}

// SetHarness records the agent the vault was last opened with, leaving the
// vault path alone. Remembering the choice is a convenience, so a failed write
// is reported but the in-memory value still moves.
func (s *Store) SetHarness(harness string) error {
	cur := s.Get()
	cur.Harness = harness
	return s.Set(cur)
}

// normalize trims whitespace and expands a leading `~/`, so a hand-typed path
// resolves the same way the shell would.
func normalize(in Settings) Settings {
	in.VaultPath = ExpandHome(strings.TrimSpace(in.VaultPath))
	in.Harness = strings.TrimSpace(in.Harness)
	in.Tasks = normalizeTasks(in.Tasks)
	return in
}

// normalizeTasks trims every field and drops blank list entries, so a list the
// user left a stray comma in does not become a filter that matches "".
//
// Folder is stored slash-separated and stripped of leading and trailing
// slashes, which is the shape the vault's own paths already use — a folder
// typed as "/Areas/" and one typed as "Areas" are the same folder, and a
// setting that treated them differently would silently scan nothing.
func normalizeTasks(in TaskSettings) TaskSettings {
	in.Folders = NormalizeFolders(in.Folders)
	in.Cutoff = strings.TrimSpace(in.Cutoff)
	in.Sections = trimmedList(in.Sections)
	in.OwnerAliases = trimmedList(in.OwnerAliases)
	return in
}

// NormalizeFolders puts each subtree in the shape the vault's own paths use:
// slash-separated, with no leading or trailing slash, blanks dropped.
//
// A folder typed as "/Areas/" and one typed as "Areas" are the same folder, and
// a setting that told them apart would silently scan nothing.
func NormalizeFolders(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, folder := range in {
		folder = strings.Trim(strings.TrimSpace(filepath.ToSlash(folder)), "/")
		if folder == "" || seen[folder] {
			continue
		}
		seen[folder] = true
		out = append(out, folder)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// trimmedList trims each entry and drops the empty ones, returning nil for a
// list with nothing left in it so the JSON stays absent rather than "[]".
func trimmedList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ExpandHome resolves a leading `~` or `~/` against the user's home directory.
// A path that does not start with `~` is returned unchanged, as is one that
// cannot be resolved (no home) — the caller's existence check reports that.
func ExpandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
