// Package reflinks holds the global "work-item reference links" setting: where a
// Jira issue key or a GitLab merge-request reference written in plain text (a
// Wiki Tasks row, say) should open. It is persisted as a small JSON file under
// the data dir (~/.ao) and edited over REST. Modeled on responselang.
//
// Every field ships EMPTY, and an empty field means "do not link that kind of
// reference": the renderer never guesses a host or a repo.
package reflinks

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const fileName = "ref-links-settings.json"

// Settings says how references resolve to URLs.
type Settings struct {
	// JiraBaseURL is the Jira site a bare issue key (ABC-123) opens on, as
	// `<JiraBaseURL>/browse/<KEY>`. Empty leaves keys as plain text.
	JiraBaseURL string `json:"jiraBaseUrl"`
	// GitLabBaseURL is the GitLab host every merge-request reference opens on.
	// Empty leaves every `!N` as plain text, aliased or not.
	GitLabBaseURL string `json:"gitlabBaseUrl"`
	// GitLabDefaultRepo is the project path (group/project) a bare `!N` means.
	// Empty leaves bare references as plain text.
	GitLabDefaultRepo string `json:"gitlabDefaultRepo"`
	// GitLabRepoAliases maps a short name written before a reference
	// (`XYZ !187`, or GitLab's own `XYZ!187`) to a project path.
	GitLabRepoAliases map[string]string `json:"gitlabRepoAliases"`
}

// projectPath is a GitLab project path: one or more `/`-separated segments of
// the characters GitLab allows in a namespace or project slug.
var projectPath = regexp.MustCompile(`^[\w.-]+(?:/[\w.-]+)*$`)

// alias is the short name that prefixes a reference in text. It must start
// with a letter so it can never be mistaken for the number it prefixes.
var alias = regexp.MustCompile(`^[A-Za-z][\w.-]*$`)

// Normalize trims every field, drops a trailing `/` from the base URLs and
// surrounding slashes from project paths, and validates the result. It returns
// the cleaned settings, or an error naming the first field that is wrong.
func Normalize(in Settings) (Settings, error) {
	out := Settings{GitLabRepoAliases: map[string]string{}}
	var err error
	if out.JiraBaseURL, err = normalizeBaseURL("Jira address", in.JiraBaseURL); err != nil {
		return Settings{}, err
	}
	if out.GitLabBaseURL, err = normalizeBaseURL("GitLab address", in.GitLabBaseURL); err != nil {
		return Settings{}, err
	}
	if out.GitLabDefaultRepo, err = normalizeProjectPath("default repo", in.GitLabDefaultRepo); err != nil {
		return Settings{}, err
	}
	seen := map[string]string{}
	for rawName, rawPath := range in.GitLabRepoAliases {
		name := strings.TrimSpace(rawName)
		if !alias.MatchString(name) {
			return Settings{}, fmt.Errorf("repo alias %q must start with a letter and hold only letters, digits, '.', '_' or '-'", rawName)
		}
		// Aliases are matched case-insensitively, so two that differ only in
		// case would be one alias pointing at two repos.
		if prev, dup := seen[strings.ToLower(name)]; dup {
			return Settings{}, fmt.Errorf("repo aliases %q and %q differ only in case", prev, name)
		}
		seen[strings.ToLower(name)] = name
		path, err := normalizeProjectPath(fmt.Sprintf("repo for alias %q", name), rawPath)
		if err != nil {
			return Settings{}, err
		}
		if path == "" {
			return Settings{}, fmt.Errorf("repo alias %q has no project path", name)
		}
		out.GitLabRepoAliases[name] = path
	}
	return out, nil
}

func normalizeBaseURL(field, raw string) (string, error) {
	s := strings.TrimRight(strings.TrimSpace(raw), "/")
	if s == "" {
		return "", nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("%s must be an http(s) URL such as https://example.com", field)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("%s must not carry a query, fragment or credentials", field)
	}
	return s, nil
}

func normalizeProjectPath(field, raw string) (string, error) {
	s := strings.Trim(strings.TrimSpace(raw), "/")
	if s == "" {
		return "", nil
	}
	if !projectPath.MatchString(s) {
		return "", fmt.Errorf("%s must be a GitLab project path such as group/project", field)
	}
	for _, segment := range strings.Split(s, "/") {
		if strings.Trim(segment, ".") == "" {
			return "", fmt.Errorf("%s must be a GitLab project path such as group/project", field)
		}
	}
	return s, nil
}

// Store is a mutex-guarded, file-backed Settings holder.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewStore loads dir/ref-links-settings.json. A missing, corrupt or invalid
// file degrades to empty settings (nothing links) rather than erroring, so the
// daemon always boots.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("reflinks: data dir is required")
	}
	s := &Store{path: filepath.Join(dir, fileName), cur: Settings{GitLabRepoAliases: map[string]string{}}}
	if b, err := os.ReadFile(s.path); err == nil {
		var loaded Settings
		if json.Unmarshal(b, &loaded) == nil {
			if clean, err := Normalize(loaded); err == nil {
				s.cur = clean
			}
		}
	}
	return s, nil
}

// Get returns a copy of the current settings.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.cur
	out.GitLabRepoAliases = make(map[string]string, len(s.cur.GitLabRepoAliases))
	for k, v := range s.cur.GitLabRepoAliases {
		out.GitLabRepoAliases[k] = v
	}
	return out
}

// Set validates, persists (atomic write via temp+rename) and updates memory.
func (s *Store) Set(next Settings) error {
	clean, err := Normalize(next)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return fmt.Errorf("reflinks: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("reflinks: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("reflinks: rename: %w", err)
	}
	s.cur = clean
	return nil
}
