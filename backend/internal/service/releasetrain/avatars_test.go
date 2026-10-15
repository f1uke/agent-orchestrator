package releasetrain

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAvatarStoreKeepsTheLastBytesPerAccount(t *testing.T) {
	s := NewAvatarStore(filepath.Join(t.TempDir(), "release-train", "avatars"))
	if err := s.Put("712020:acc-ann", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("712020:acc-ann", []byte("two")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("712020:acc-ann")
	if err != nil || !bytes.Equal(got, []byte("two")) {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if _, err := s.Get("acc-bob"); !errors.Is(err, ErrNoAvatar) {
		t.Fatalf("Get of an account never stored = %v, want ErrNoAvatar", err)
	}
}

func TestAvatarStoreRefusesAccountIdsThatLeaveItsDirectory(t *testing.T) {
	root := t.TempDir()
	s := NewAvatarStore(filepath.Join(root, "avatars"))
	if err := os.WriteFile(filepath.Join(root, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "..", "../secret", "a/b", `a\b`, ".hidden", "a b"} {
		if err := s.Put(id, []byte("x")); !errors.Is(err, ErrBadAccountID) {
			t.Errorf("Put(%q) = %v, want ErrBadAccountID", id, err)
		}
		if _, err := s.Get(id); !errors.Is(err, ErrBadAccountID) {
			t.Errorf("Get(%q) = %v, want ErrBadAccountID", id, err)
		}
	}
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Fatalf("root holds %d entries, want only the secret", len(entries))
	}
}
