// Package fsatomic replaces a file's contents without ever leaving a torn one
// behind.
//
// It exists because two write paths need the same guarantee against the same
// hazard: an AO worktree and a note vault both have agents reading and writing
// files while the app writes them, so a reader observing a half-written file is
// a routine event rather than a rare one. Keeping one implementation means the
// guarantee cannot silently differ between them.
package fsatomic

import (
	"os"
	"path/filepath"
)

// WriteFile replaces a file's contents through a same-directory temp file and a
// rename, so a concurrent reader - an agent, a build - never observes a
// half-written file. The mode is applied to the temp file before the rename, so
// the replacement carries the permission bits the caller asked for (normally
// the original's).
//
// The rename gives the file a new inode, which breaks any hard link to it. That
// is the accepted cost of never leaving a torn file behind.
func WriteFile(target string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".ao-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	// Removed on every failure path; a no-op once the rename has moved it.
	defer func() { _ = os.Remove(name) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, target)
}
