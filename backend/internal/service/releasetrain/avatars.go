package releasetrain

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

var (
	// ErrBadAccountID means an account id is not one Jira issues, so it cannot name a file.
	ErrBadAccountID = errors.New("release trains: bad account id")
	// ErrNoAvatar means no avatar has been downloaded for the account yet.
	ErrNoAvatar = errors.New("release trains: no avatar for this account")
)

var accountIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_-]{0,127}$`)

// AvatarStore keeps one downloaded avatar per Jira account under Dir.
type AvatarStore struct{ Dir string }

// NewAvatarStore returns a store under dir; the directory is made on the first Put.
func NewAvatarStore(dir string) *AvatarStore { return &AvatarStore{Dir: dir} }

func (s *AvatarStore) path(accountID string) (string, error) {
	if !accountIDPattern.MatchString(accountID) {
		return "", fmt.Errorf("%w: %q", ErrBadAccountID, accountID)
	}
	return filepath.Join(s.Dir, accountID), nil
}

// Put replaces the account's avatar; a reader never sees a half-written file.
func (s *AvatarStore) Put(accountID string, b []byte) error {
	p, err := s.path(accountID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Get returns the account's avatar bytes, or ErrNoAvatar.
func (s *AvatarStore) Get(accountID string) ([]byte, error) {
	p, err := s.path(accountID)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p) //nolint:gosec // G304: the name passed accountIDPattern, so it stays inside Dir
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoAvatar
	}
	return b, err
}
