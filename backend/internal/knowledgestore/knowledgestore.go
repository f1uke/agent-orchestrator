// Package knowledgestore preserves the durable planning artifacts a worker may
// leave in its worktree (plans, proposals, diagnosis write-ups) into AO's
// private, per-project knowledge store so they survive worktree teardown.
//
// The store lives OUTSIDE any project repo — under the AO data dir
// (<dataDir>/knowledge/<project>, i.e. ~/.ao/knowledge/<project> by default) —
// and is never committed or pushed. This is the belt-and-suspenders safety net
// behind the worker prompt, which asks agents to write these artifacts to the
// store directly as they go; this package only catches strays left behind in
// the worktree at teardown.
package knowledgestore

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PlansDir returns the per-project plans directory inside the knowledge store:
// <dataDir>/knowledge/<projectID>/plans. dataDir is AO's resolved data dir
// (~/.ao by default), so the store follows an AO_DATA_DIR override the same way
// the rest of AO's state does.
func PlansDir(dataDir, projectID string) string {
	return filepath.Join(dataDir, "knowledge", projectID, "plans")
}

// maxSuffixAttempts bounds how many numeric suffixes a differing same-named
// artifact is tried under before giving up, so a pathological run can't loop.
const maxSuffixAttempts = 100

// PreserveStrayDocs scans worktreePath for stray planning documents and copies
// each into destPlansDir, prefixed with the branch slug. It NEVER overwrites an
// existing preserved file: identical content is treated as already-preserved
// (a no-op), and differing content for the same source name is written under a
// numeric suffix. destPlansDir is created lazily, only when there is something
// to copy.
//
// It is best-effort. A missing or unreadable worktreePath is a benign no-op
// (nil, nil). Per-file failures are collected into the returned error while the
// scan continues; callers should log that error and never fail teardown on it.
// The returned slice holds the absolute paths actually written this call.
func PreserveStrayDocs(worktreePath, branch, destPlansDir string) (written []string, err error) {
	var errs []error
	walkErr := filepath.WalkDir(worktreePath, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Unreadable entry: skip it (skip the whole subtree if it's a dir)
			// and keep scanning the rest — this is a best-effort safety net.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != worktreePath && shouldSkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(worktreePath, p)
		if relErr != nil || !isStrayDoc(rel) {
			return nil
		}
		dst, copyErr := copyPreserve(p, rel, branch, destPlansDir)
		if copyErr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, copyErr))
			return nil
		}
		if dst != "" {
			written = append(written, dst)
		}
		return nil
	})
	// A root that does not exist surfaces here as a non-nil walkErr only when the
	// callback never ran; treat any such top-level error as benign (no-op).
	if walkErr != nil && !errors.Is(walkErr, fs.ErrNotExist) {
		errs = append(errs, walkErr)
	}
	return written, errors.Join(errs...)
}

// isStrayDoc reports whether the worktree-relative path is a planning artifact
// worth preserving: any `*plan*.md` / `*proposal*.md` file (case-insensitive),
// or any `.md` directly under a `docs/plans/` directory.
func isStrayDoc(rel string) bool {
	rel = strings.ToLower(filepath.ToSlash(rel))
	if !strings.HasSuffix(rel, ".md") {
		return false
	}
	dir, base := path2(rel)
	if dir == "docs/plans" || strings.HasSuffix(dir, "/docs/plans") {
		return true
	}
	return strings.Contains(base, "plan") || strings.Contains(base, "proposal")
}

// path2 splits a forward-slash path into its directory and base name.
func path2(p string) (dir, base string) {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "", p
	}
	return p[:i], p[i+1:]
}

// shouldSkipDir reports whether a directory should be skipped entirely: known
// heavy build/dependency dirs and any hidden dir (e.g. .git, .claude), none of
// which hold artifacts a worker authored for humans.
func shouldSkipDir(name string) bool {
	switch name {
	case "node_modules", "vendor", "dist", "build", "target", "out":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// copyPreserve copies src into destPlansDir under "<branchSlug>--<relSlug>",
// creating destPlansDir on first write. Returns the written path, or "" when an
// identical copy already exists (idempotent no-op).
func copyPreserve(src, rel, branch, destPlansDir string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	name := slug(branch) + "--" + slug(filepath.ToSlash(rel))
	stem, ext := splitExt(name)
	if err := os.MkdirAll(destPlansDir, 0o750); err != nil {
		return "", err
	}
	for i := 0; i < maxSuffixAttempts; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d%s", stem, i+1, ext)
		}
		dst := filepath.Join(destPlansDir, candidate)
		existing, readErr := os.ReadFile(dst)
		switch {
		case os.IsNotExist(readErr):
			if err := os.WriteFile(dst, data, 0o640); err != nil {
				return "", err
			}
			return dst, nil
		case readErr != nil:
			return "", readErr
		case bytes.Equal(existing, data):
			return "", nil // already preserved verbatim: nothing to do
		}
		// A different file already claims this name: try the next suffix.
	}
	return "", fmt.Errorf("too many colliding preserved copies for %q", name)
}

// slug makes a path or branch safe for use as a single filename segment by
// replacing path separators and whitespace with hyphens. An empty input yields
// a stable placeholder so a copy is still produced.
func slug(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "nobranch"
	}
	s = filepath.ToSlash(s)
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', ' ', '\t', '\\':
			return '-'
		}
		return r
	}, s)
}

// splitExt splits name into its stem and extension (including the dot). A name
// with no dot returns the whole name as the stem and an empty extension.
func splitExt(name string) (stem, ext string) {
	i := strings.LastIndex(name, ".")
	if i <= 0 {
		return name, ""
	}
	return name[:i], name[i:]
}
