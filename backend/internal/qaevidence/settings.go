// Package qaevidence holds the global QA evidence setting: the Google Drive
// folder, as an rclone path, that a run's QA Evidence folder is uploaded into.
// It is persisted as a small JSON file under the data dir (~/.ao) and edited
// over REST. Modeled on reflinks.
//
// It is global, not per project: the local tree ~/Desktop/QA Evidence/ is one
// per Mac and starts with the Testiny project's name, so it maps to one Drive
// root, and the rclone remote is this Mac's credential. It ships empty, and
// empty means evidence upload is off.
package qaevidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const fileName = "qa-evidence-settings.json"

// Settings is where QA evidence is uploaded.
type Settings struct {
	// DriveFolder is an rclone path, <remote>:<path>, e.g. finnomena:QA. The
	// path may be empty (the remote's root). Empty turns upload off.
	DriveFolder string `json:"driveFolder"`
}

// remoteName is what rclone accepts as a remote's name: letters, digits, '_',
// '-', '.', '+', '@' and spaces, not starting with '-' or a space and not
// ending with a space.
var remoteName = regexp.MustCompile(`^[\pL\pN_.+@][\pL\pN_.+@ -]*$`)

// Normalize trims the folder, drops trailing '/' from its path, and validates
// it. It returns the cleaned settings, or an error saying what is wrong.
func Normalize(in Settings) (Settings, error) {
	folder := strings.TrimSpace(in.DriveFolder)
	if folder == "" {
		return Settings{}, nil
	}
	remote, path, ok := strings.Cut(folder, ":")
	if !ok || remote == "" {
		return Settings{}, fmt.Errorf("drive folder %q must be an rclone path such as finnomena:QA (<remote>:<path>)", in.DriveFolder)
	}
	if !remoteName.MatchString(remote) || strings.HasSuffix(remote, " ") {
		return Settings{}, fmt.Errorf("drive folder %q names remote %q, which is not an rclone remote name", in.DriveFolder, remote)
	}
	if strings.ContainsAny(path, "\x00\n\r") {
		return Settings{}, fmt.Errorf("drive folder %q has a control character in its path", in.DriveFolder)
	}
	return Settings{DriveFolder: remote + ":" + strings.TrimRight(path, "/")}, nil
}

// Remote is the rclone remote the folder is on, or "" when upload is off.
func (s Settings) Remote() string {
	remote, _, _ := strings.Cut(s.DriveFolder, ":")
	return remote
}

// Join is the rclone path of rel ('/'-separated) inside the folder.
func (s Settings) Join(rel string) string {
	if strings.HasSuffix(s.DriveFolder, ":") {
		return s.DriveFolder + rel
	}
	return s.DriveFolder + "/" + rel
}

// Store is a mutex-guarded, file-backed Settings holder.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewStore loads dir/qa-evidence-settings.json. A missing, corrupt or invalid
// file degrades to empty settings (upload off) rather than erroring, so the
// daemon always boots.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("qaevidence: data dir is required")
	}
	s := &Store{path: filepath.Join(dir, fileName)}
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

// Get returns the current settings.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Set validates, persists (atomic write via temp+rename) and updates memory.
func (s *Store) Set(next Settings) error {
	clean, err := Normalize(next)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return fmt.Errorf("qaevidence: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("qaevidence: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("qaevidence: rename: %w", err)
	}
	s.cur = clean
	return nil
}
