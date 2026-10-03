// Package knowledgestore preserves the durable planning artifacts a worker may
// leave in its worktree (plans, proposals, diagnosis write-ups) into AO's
// private, per-project knowledge store so they survive worktree teardown.
//
// The store lives OUTSIDE any project repo, at ~/.ao/knowledge/<project>, and
// is never committed or pushed. This is the belt-and-suspenders safety net
// behind the worker prompt, which asks agents to write these artifacts to the
// store directly as they go; this package only catches strays left behind in
// the worktree at teardown.
package knowledgestore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// HomeRelDir is where the knowledge store sits relative to the account's home
// directory. It is the ONE definition of the store's location: the agent
// prompts name it as PromptDir and the daemon resolves it with Root, so what an
// agent is told to read is exactly where AO writes.
//
// It deliberately does NOT follow AO_DATA_DIR. The store is the human's
// curated knowledge, shared by every daemon on the account - a sandbox daemon a
// worker runs from its branch included - and the prompts (and every saved
// prompt override) name it by its home-relative path, which an agent's shell
// resolves against HOME. A data-dir-relative store is what stranded hundreds
// of rescued docs under ~/.ao/data/knowledge where no agent ever looked.
const HomeRelDir = ".ao/knowledge"

// PromptDir is the store root as the agent prompts spell it.
const PromptDir = "~/" + HomeRelDir

// Root returns the knowledge store root under the given home directory.
func Root(home string) string {
	return filepath.Join(home, filepath.FromSlash(HomeRelDir))
}

// PlansDir returns the per-project plans directory inside the knowledge store
// rooted at root: <root>/<projectID>/plans.
func PlansDir(root, projectID string) string {
	return filepath.Join(root, projectID, "plans")
}

// maxSuffixAttempts bounds how many numeric suffixes a differing same-named
// artifact is tried under before giving up, so a pathological run can't loop.
const maxSuffixAttempts = 100

// PreserveStrayDocs scans worktreePath for stray planning documents and copies
// each into destPlansDir, prefixed with the branch slug. A document whose exact
// content is already committed at its path on some branch of the repository is
// skipped: git keeps it, and copying it again from every branch is how one
// tracked doc used to land in the store once per branch. Only genuinely
// uncommitted docs - untracked, or tracked with edits nobody committed - are
// worth rescuing.
//
// It NEVER overwrites an existing preserved file: identical content is treated
// as already-preserved (a no-op), and differing content for the same source
// name is written under a numeric suffix. destPlansDir is created lazily, only
// when there is something to copy.
//
// It is best-effort. A missing or unreadable worktreePath is a benign no-op
// (nil, nil). Per-file failures are collected into the returned error while the
// scan continues; callers should log that error and never fail teardown on it.
// When git cannot say what is committed, every candidate is copied (a spare
// copy beats a lost plan) and the git failure is reported in the error. The
// returned slice holds the absolute paths actually written this call.
func PreserveStrayDocs(worktreePath, branch, destPlansDir string) (written []string, err error) {
	var errs []error
	var candidates []string // worktree-relative, OS separators
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
		if relErr != nil {
			//nolint:nilerr // best-effort: skip an unrelatable path, keep scanning
			return nil
		}
		if isStrayDoc(rel) {
			candidates = append(candidates, rel)
		}
		return nil
	})
	// A root that does not exist surfaces here as a non-nil walkErr only when the
	// callback never ran; treat any such top-level error as benign (no-op).
	if walkErr != nil && !errors.Is(walkErr, fs.ErrNotExist) {
		errs = append(errs, walkErr)
	}

	committed, commitErr := committedCandidates(context.Background(), worktreePath, candidates)
	if commitErr != nil {
		errs = append(errs, fmt.Errorf("tell committed docs apart, copying every candidate: %w", commitErr))
	}
	for _, rel := range candidates {
		if committed[rel] {
			continue
		}
		dst, copyErr := copyPreserve(filepath.Join(worktreePath, rel), rel, branch, destPlansDir)
		if copyErr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, copyErr))
			continue
		}
		if dst != "" {
			written = append(written, dst)
		}
	}
	return written, errors.Join(errs...)
}

// committedCandidates reports which worktree-relative candidates hold content
// that is already committed at that same path on some branch (see
// committedBlobs). A worktree that is not a git checkout has nothing
// committed: every candidate is a rescue, and that is not an error.
func committedCandidates(ctx context.Context, worktreePath string, candidates []string) (map[string]bool, error) {
	if len(candidates) == 0 || !isGitRepo(ctx, worktreePath) {
		return nil, nil
	}
	gitPaths := make([]string, len(candidates))
	absPaths := make([]string, len(candidates))
	for i, rel := range candidates {
		gitPaths[i] = filepath.ToSlash(rel)
		absPaths[i] = filepath.Join(worktreePath, rel)
	}
	ids, err := hashFiles(ctx, worktreePath, absPaths)
	if err != nil {
		return nil, err
	}
	blobs, err := committedBlobs(ctx, worktreePath, gitPaths)
	if err != nil {
		return nil, err
	}
	committed := map[string]bool{}
	for i, rel := range candidates {
		if blobs[gitPaths[i]][ids[i]] {
			committed[rel] = true
		}
	}
	return committed, nil
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
	if err := os.MkdirAll(destPlansDir, 0o750); err != nil {
		return "", err
	}
	dst, wrote, err := placeNoClobber(destPlansDir, name, data, nil)
	if err != nil || !wrote {
		return "", err
	}
	return dst, nil
}

// placeNoClobber writes data into dir under name without ever replacing a file
// already there. If name holds identical bytes the data is already in place and
// nothing is written (wrote=false, dst is that file). If it holds different
// bytes the next free "<stem>-N<ext>" (N from 2) is tried the same way. Each
// write is an exclusive create, so a concurrent writer can never be clobbered
// between the check and the write.
//
// reserved, when non-nil, marks names a dry run has already handed out: they
// are treated as taken, and the chosen name is added. Nothing touches disk when
// reserved is non-nil.
func placeNoClobber(dir, name string, data []byte, reserved map[string]bool) (dst string, wrote bool, err error) {
	stem, ext := splitExt(name)
	for i := 0; i < maxSuffixAttempts; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d%s", stem, i+1, ext)
		}
		dst = filepath.Join(dir, candidate)
		if reserved != nil && reserved[candidate] {
			continue
		}
		existing, readErr := os.ReadFile(dst)
		switch {
		case readErr == nil && bytes.Equal(existing, data):
			return dst, false, nil // already in place verbatim: nothing to do
		case readErr == nil:
			continue // a different file already claims this name
		case !errors.Is(readErr, fs.ErrNotExist):
			return "", false, readErr
		}
		if reserved != nil {
			reserved[candidate] = true
			return dst, true, nil
		}
		created, createErr := writeExclusive(dst, data)
		if createErr != nil {
			return "", false, createErr
		}
		if created {
			return dst, true, nil
		}
		// Lost a race for this name: the winner may have written the same bytes.
		if existing, readErr := os.ReadFile(dst); readErr == nil && bytes.Equal(existing, data) {
			return dst, false, nil
		}
	}
	return "", false, fmt.Errorf("too many colliding copies for %q", name)
}

// writeExclusive creates path with data, refusing to touch an existing file.
// created=false with a nil error means the path appeared in the meantime.
func writeExclusive(path string, data []byte) (created bool, err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return false, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return false, err
	}
	return true, nil
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
