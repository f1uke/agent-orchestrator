package gitworktree

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// This file separates the two things `git status --porcelain` lumps together on
// a finished worktree: work that exists nowhere else, and build output a
// rebuild reproduces from committed sources.
//
// The distinction matters because a native-app worktree grows a
// `derivedDataPath/` on its first build and is therefore reported dirty for the
// rest of its life. Treating that as "uncommitted work" pins the worktree —
// and its many gigabytes — on disk permanently, long after the session that
// owned it merged.
//
// Only UNTRACKED entries can ever be classified as regenerable. A tracked file
// that is modified, staged, renamed or deleted is human work by definition, no
// matter what it is called or which directory it sits in.

// statusEntry is one parsed record of `git status --porcelain -z`.
type statusEntry struct {
	// code is the two-character XY status field ("??" for untracked).
	code string
	// rel is the worktree-relative path, unquoted (-z emits raw bytes).
	rel string
}

// parseStatusZ parses NUL-separated `git status --porcelain -z` output.
//
// -z rather than plain --porcelain because the plain form C-quotes any path
// with a space, a quote or a non-ASCII byte, which would make a path like
// `My Project/DerivedData` unparseable by field splitting. -z emits paths raw.
// Rename and copy records (R/C) carry a SECOND NUL-terminated field holding the
// original path; it is consumed here so it is never mistaken for its own entry.
func parseStatusZ(out string) []statusEntry {
	fields := strings.Split(out, "\x00")
	entries := make([]statusEntry, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		// A record is "XY<space><path>": at least 4 bytes. The trailing empty
		// field after the final NUL lands here too and is skipped by this check.
		if len(f) < 4 {
			continue
		}
		code := f[:2]
		rel := f[3:]
		if code[0] == 'R' || code[0] == 'C' {
			i++ // consume the origin-path field that follows a rename/copy
		}
		entries = append(entries, statusEntry{code: code, rel: rel})
	}
	return entries
}

// isRegenerable reports whether an untracked path is build output covered by
// one of the patterns.
//
// A pattern matches when it equals one of the path's segments, so a nested
// `fastlane/xcov_report/` is caught by the bare pattern `xcov_report`, or when
// path.Match accepts the whole relative path, which is what gives glob patterns
// like `*.xcresult` their reach. Segment equality is deliberately exact — a
// pattern `Pods` must not match a directory named `PodsHelper`.
func isRegenerable(rel string, patterns []string) bool {
	rel = strings.TrimSuffix(strings.TrimPrefix(rel, "./"), "/")
	if rel == "" {
		return false
	}
	segments := strings.Split(rel, "/")
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		for _, seg := range segments {
			if seg == pattern {
				return true
			}
			if ok, err := path.Match(pattern, seg); err == nil && ok {
				return true
			}
		}
		if ok, err := path.Match(pattern, rel); err == nil && ok {
			return true
		}
	}
	return false
}

// inProgressOpFiles are the git-dir entries whose presence means a multi-step
// operation is paused mid-flight in this worktree.
//
// These are checked separately from `git status` because an interactive rebase
// stopped at an `edit` step, or a bisect run, can leave a CLEAN working tree
// while still holding state — a todo list, ORIG_HEAD, and commits referenced
// only from the rebase directory — that lives in the worktree's git dir and is
// destroyed with it. A stash is deliberately NOT on this list: `git stash`
// writes to the repository's shared refs/stash, which outlives the worktree, so
// stashed work is not lost by reclaiming one.
var inProgressOpFiles = []string{
	"MERGE_HEAD",
	"CHERRY_PICK_HEAD",
	"REVERT_HEAD",
	"BISECT_LOG",
	"rebase-merge",
	"rebase-apply",
}

// inProgressOp returns the name of a paused git operation in the worktree, or
// "" if none. A probe that cannot be run is reported as no-op-found; the caller
// still has the `git status` result and the non-force remove as backstops.
func (w *Workspace) inProgressOp(ctx context.Context, worktree string) string {
	for _, name := range inProgressOpFiles {
		out, err := w.run(ctx, w.binary, revParseGitPathArgs(worktree, name)...)
		if err != nil {
			continue
		}
		resolved := strings.TrimSpace(string(out))
		if resolved == "" {
			continue
		}
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(worktree, resolved)
		}
		if _, statErr := os.Lstat(resolved); statErr == nil {
			return name
		}
	}
	return ""
}

// clearRegenerableArtifacts deletes untracked build output from a worktree that
// is about to be torn down, but ONLY when doing so leaves nothing else dirty.
//
// Every branch out of here is a no-op on doubt. It gives up if the feature is
// unconfigured, if the status probe fails, if any entry is real work, if no
// entry is an artefact, or if a git operation is paused in the worktree. That
// asymmetry is deliberate: failing to clear costs disk, and clearing wrongly
// costs a rebuild, so uncertainty always resolves towards keeping the files.
//
// It is best-effort by design — the caller's non-force `git worktree remove` is
// what actually decides whether the worktree goes, and it independently refuses
// anything still dirty.
func (w *Workspace) clearRegenerableArtifacts(ctx context.Context, worktree string) {
	patterns := w.artifactPatterns()
	if len(patterns) == 0 {
		return
	}
	out, err := w.run(ctx, w.binary, statusPorcelainZArgs(worktree)...)
	if err != nil {
		return
	}
	blocking, artifacts := classifyStatus(string(out), patterns)
	if len(blocking) > 0 || len(artifacts) == 0 {
		return
	}
	if op := w.inProgressOp(ctx, worktree); op != "" {
		return
	}
	_ = removeArtifacts(worktree, artifacts)
}

// removeArtifacts deletes the classified regenerable paths from the worktree.
//
// It is called only once the worktree is known to be otherwise clean and about
// to be removed wholesale, so these bytes are going away either way — clearing
// them first is what lets the non-force `git worktree remove` succeed instead
// of refusing. Each path is confined to the worktree before deletion so a
// crafted status line cannot walk out of it.
func removeArtifacts(worktree string, rels []string) error {
	for _, rel := range rels {
		target := filepath.Join(worktree, filepath.FromSlash(rel))
		if !isConfinedTo(worktree, target) {
			continue
		}
		if err := os.RemoveAll(target); err != nil {
			return err
		}
	}
	return nil
}

// isConfinedTo reports whether target lies inside root.
func isConfinedTo(root, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// classifyStatus splits porcelain output into the entries that must BLOCK
// teardown and the untracked artefact paths that may be cleared out of its way.
//
// Anything not positively identified as regenerable build output is blocking.
// That default is the safety property: an unrecognised entry, an unparseable
// line, or an empty pattern list all steer towards refusing to delete.
func classifyStatus(out string, patterns []string) (blocking, artifacts []string) {
	for _, e := range parseStatusZ(out) {
		if e.code == "??" && isRegenerable(e.rel, patterns) {
			artifacts = append(artifacts, e.rel)
			continue
		}
		blocking = append(blocking, e.rel)
	}
	return blocking, artifacts
}
