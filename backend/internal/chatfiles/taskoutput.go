package chatfiles

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

var taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidTaskID reports whether id is in the charset claude uses for task ids.
func ValidTaskID(id string) bool { return taskIDPattern.MatchString(id) }

// ClaudeTempRoots are the symlink-resolved directories Claude Code writes task
// output under: $CLAUDE_CODE_TMPDIR/claude-<uid> when that is set, and
// /tmp/claude-<uid>, its default (2.1.x ignores $TMPDIR here). A missing
// directory is left out.
func ClaudeTempRoots() []string {
	name := fmt.Sprintf("claude-%d", os.Getuid())
	var roots []string
	for _, dir := range []string{os.Getenv("CLAUDE_CODE_TMPDIR"), "/tmp"} {
		if dir == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			continue
		}
		if root := filepath.Join(resolved, name); !slices.Contains(roots, root) {
			roots = append(roots, root)
		}
	}
	return roots
}

// OpenTaskOutput opens the output file claude named for taskID, when after
// symlinks it is a regular file named <taskID>.output, in a directory named
// tasks whose parent is named for one of claudeSessionIDs, under one of
// ClaudeTempRoots. Every refusal is ErrNotFound.
func OpenTaskOutput(path, taskID string, claudeSessionIDs []string) (*os.File, os.FileInfo, error) {
	if !ValidTaskID(taskID) {
		return nil, nil, ErrNotFound
	}
	for _, root := range ClaudeTempRoots() {
		resolved, _, info, err := Within(root, path)
		if err != nil {
			continue
		}
		tasks := filepath.Dir(resolved)
		if filepath.Base(resolved) != taskID+".output" || filepath.Base(tasks) != "tasks" ||
			!slices.Contains(claudeSessionIDs, filepath.Base(filepath.Dir(tasks))) {
			return nil, nil, ErrNotFound
		}
		f, err := os.Open(resolved) //nolint:gosec // G304: confined above
		if err != nil {
			return nil, nil, ErrNotFound
		}
		// The file must still be the one checked, not a link swapped in since.
		if opened, err := f.Stat(); err != nil || !os.SameFile(info, opened) {
			_ = f.Close()
			return nil, nil, ErrNotFound
		}
		return f, info, nil
	}
	return nil, nil, ErrNotFound
}
