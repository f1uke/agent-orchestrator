package session

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/diffhunk"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
)

// maxResolveCandidates bounds how many candidate paths a ref resolves to, so a
// common basename in a huge tree can't return an unbounded list.
const maxResolveCandidates = 50

// maxWorkspaceFileBytes caps how large a file the viewer will read. The cap is
// checked against the stat size BEFORE the file is read, so a huge blob is never
// pulled into memory.
const maxWorkspaceFileBytes = 5 << 20 // 5 MiB

// Reasons a file resolved but cannot be shown in the text viewer.
const (
	UnavailableTooLarge = "too_large"
	UnavailableBinary   = "binary"
)

// WorkspaceFileResult is a file's content plus the per-line map of its
// uncommitted changes (working tree vs HEAD).
type WorkspaceFileResult struct {
	Available bool
	// Path is workspace-relative (slash-separated) for a file inside the
	// session's workspace, and an absolute path for one outside it.
	Path         string
	Lines        []DiffContextLine
	ChangedLines []diffhunk.LineChange
	Truncated    bool
	// Reason explains an Available=false result (UnavailableTooLarge,
	// UnavailableBinary); empty when the file is displayable.
	Reason string
}

// refTarget classifies a terminal file reference by SHAPE and, for the two
// shapes that name a location on disk, returns the absolute path they point at:
//
//   - absolute (`/a/b.go`) and tilde (`~/a/b.go`) name a location globally and
//     return (abs, true);
//   - relative (`pkg/a.go`) and bare (`a.go`) have no meaning outside a
//     workspace and return ("", false), keeping their #127 workspace-scoped
//     resolution.
//
// A `~` only expands as the whole ref or as a leading `~/` segment, so a path
// segment that merely contains a tilde (`dir/~backup.md`) stays relative.
func refTarget(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	if ref == "~" || strings.HasPrefix(ref, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", false
		}
		rest := strings.TrimPrefix(strings.TrimPrefix(ref, "~"), "/")
		if rest == "" {
			return filepath.Clean(home), true
		}
		return filepath.Join(home, filepath.FromSlash(rest)), true
	}
	if filepath.IsAbs(ref) {
		return filepath.Clean(ref), true
	}
	return "", false
}

// resolveTarget maps an absolute target to the real file it names: symlinks are
// followed (the resolved target is what gets read and displayed) and the result
// must be a regular file. A missing path, a broken symlink, or a directory
// yields ok=false, which the caller degrades to "no candidates".
func resolveTarget(abs string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", false
	}
	if !isRegularFile(resolved) {
		return "", false
	}
	return resolved, true
}

// ResolveWorkspaceRef maps a file reference printed in the terminal to the
// candidate paths it can open. Resolution splits by ref shape:
//
//   - An ABSOLUTE or `~/` ref resolves to that exact path anywhere on disk,
//     with NO workspace confinement (see the note on the absolute branch).
//   - A RELATIVE or BARE ref stays workspace-scoped — such a ref has no meaning
//     outside a workspace — and may return several candidates for the UI picker.
//
// Zero candidates (no match) is not an error; only an unknown session is.
func (s *Service) ResolveWorkspaceRef(ctx context.Context, id domain.SessionID, ref string) ([]string, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return nil, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	workspace := rec.Metadata.WorkspacePath
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}

	// INTENTIONALLY UNCONFINED — approved product decision, do not "fix" back to
	// a workspace-confined resolve. An absolute or `~/` path is meant to open
	// wherever it points (a knowledge-store note, another session's worktree),
	// so it is deliberately NOT checked against the session's workspace. The
	// daemon stays loopback-only, which remains the containing boundary. Note
	// there is no basename fallback here on purpose: an absolute ref that does
	// not exist must not silently open a same-named file inside the workspace.
	if abs, isAbs := refTarget(ref); isAbs {
		resolved, ok := resolveTarget(abs)
		if !ok {
			return nil, nil
		}
		// A target that happens to live inside this session's workspace is
		// reported workspace-relative, so the viewer shows the short path (and
		// #127's in-worktree behaviour is unchanged).
		if rel, within := relWithin(resolvedRoot(workspace), resolved); within {
			return []string{filepath.ToSlash(rel)}, nil
		}
		return []string{resolved}, nil
	}

	if workspace == "" {
		return nil, nil
	}

	if strings.ContainsAny(ref, "/\\") {
		clean := filepath.ToSlash(ref)
		if abs, ok := previewutil.ConfinedPath(workspace, clean); ok && isRegularFile(abs) {
			if rel, within := relWithin(absRoot(workspace), abs); within {
				return []string{filepath.ToSlash(rel)}, nil
			}
		}
		// Not present at that exact relative location: try a path-suffix match
		// (the ref may be rooted deeper than the workspace), then basename.
		if cands := s.searchWorkspaceFiles(ctx, workspace, clean, true); len(cands) > 0 {
			return cands, nil
		}
		return s.searchWorkspaceFiles(ctx, workspace, path.Base(clean), false), nil
	}

	return s.searchWorkspaceFiles(ctx, workspace, ref, false), nil
}

// searchWorkspaceFiles returns workspace-relative paths matching needle. When
// bySuffix is true, needle is a path suffix (`dir/file.ext`); otherwise it is a
// bare basename. Results are sorted and capped.
func (s *Service) searchWorkspaceFiles(ctx context.Context, workspace, needle string, bySuffix bool) []string {
	needle = strings.TrimPrefix(filepath.ToSlash(needle), "/")
	if needle == "" {
		return nil
	}
	files := workspaceFileIndex(ctx, workspace)
	var out []string
	for _, f := range files {
		var match bool
		if bySuffix {
			match = f == needle || strings.HasSuffix(f, "/"+needle)
		} else {
			match = path.Base(f) == needle
		}
		if match {
			out = append(out, f)
			if len(out) >= maxResolveCandidates {
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// workspaceFileIndex lists workspace-relative paths (tracked + untracked, minus
// ignored) via git, falling back to a bounded walk for a non-git workspace.
func workspaceFileIndex(ctx context.Context, workspace string) []string {
	out, err := gitOutput(ctx, workspace, "ls-files", "-co", "--exclude-standard", "-z")
	if err == nil {
		var files []string
		for _, p := range strings.Split(string(out), "\x00") {
			if p != "" {
				files = append(files, filepath.ToSlash(p))
			}
		}
		return files
	}
	return walkWorkspaceFiles(workspace)
}

// walkWorkspaceFiles is the non-git fallback index: a bounded walk that skips
// dot-directories and node_modules and caps its result.
func walkWorkspaceFiles(workspace string) []string {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return nil
	}
	const walkCap = 20000
	var files []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip unreadable entries, keep walking
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil //nolint:nilerr // unrelatable path, skip
		}
		files = append(files, filepath.ToSlash(rel))
		if len(files) >= walkCap {
			return filepath.SkipAll
		}
		return nil
	})
	return files
}

// ReadWorkspaceFile reads a file's content and computes its per-line
// uncommitted-change map. The path is interpreted with the same shape split as
// ResolveWorkspaceRef: an absolute or `~/` path is read wherever it points
// (unconfined, by design); anything else is resolved inside the session's
// workspace and may not escape it. A missing/non-regular file is NotFound, as
// is an unknown session. A file outside any git repo yields no change markers.
func (s *Service) ReadWorkspaceFile(ctx context.Context, id domain.SessionID, filePath string) (WorkspaceFileResult, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return WorkspaceFileResult{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return WorkspaceFileResult{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}

	// INTENTIONALLY UNCONFINED — approved product decision (see the matching
	// note in ResolveWorkspaceRef). An absolute or `~/` path is read wherever it
	// points, with no workspace containment check.
	if abs, isAbs := refTarget(filePath); isAbs {
		resolved, ok := resolveTarget(abs)
		if !ok {
			return WorkspaceFileResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "File not found")
		}
		return readFileForViewer(ctx, resolved, filepath.ToSlash(resolved))
	}

	// Relative / bare paths stay confined to the session's workspace.
	workspace := rec.Metadata.WorkspacePath
	if workspace == "" {
		return WorkspaceFileResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "File not found in workspace")
	}
	confined, ok := previewutil.ConfinedPath(workspace, filePath)
	if !ok {
		return WorkspaceFileResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "File not found in workspace")
	}
	rel, err := filepath.Rel(absRoot(workspace), confined)
	if err != nil {
		return WorkspaceFileResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "File not found in workspace")
	}
	return readFileForViewer(ctx, confined, filepath.ToSlash(rel))
}

// readFileForViewer turns a resolved file into viewer content: a size cap
// checked against the stat size BEFORE reading, binary detection, the line cap,
// and the per-line uncommitted-change map. An unreadable file is NotFound; a
// file that exists but cannot be rendered comes back Available=false with a
// Reason, so the viewer can say why rather than failing.
func readFileForViewer(ctx context.Context, abs, display string) (WorkspaceFileResult, error) {
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return WorkspaceFileResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "File not found")
	}
	if info.Size() > maxWorkspaceFileBytes {
		return WorkspaceFileResult{Path: display, Reason: UnavailableTooLarge}, nil
	}
	data, err := os.ReadFile(abs) //nolint:gosec // reading an arbitrary absolute path is the intended behaviour here
	if err != nil {
		return WorkspaceFileResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "File not found")
	}
	if isBinary(data) {
		return WorkspaceFileResult{Path: display, Reason: UnavailableBinary}, nil
	}
	res := workspaceFileContent(display, string(data))
	res.ChangedLines = uncommittedChanges(ctx, abs, len(res.Lines))
	return res, nil
}

// workspaceFileContent numbers a file blob as context lines, capping at
// maxFileLines (shared with the diff-context file mode).
func workspaceFileContent(relPath, content string) WorkspaceFileResult {
	rows := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	res := WorkspaceFileResult{Available: true, Path: relPath}
	for i, r := range rows {
		if i >= maxFileLines {
			res.Truncated = true
			break
		}
		res.Lines = append(res.Lines, DiffContextLine{Kind: "context", NewLine: i + 1, Text: r})
	}
	return res
}

// uncommittedChanges returns the per-line change map of the working tree vs
// HEAD for one file, probed in whatever git repository contains it (its own
// directory is the git working dir, and the file is named by absolute path).
// That covers all three cases with one code path: a file in the session's
// workspace, a file in a DIFFERENT repo (another session's worktree — markers
// come from that repo), and a file in no repo at all (a knowledge-store note),
// where git errors out and the viewer simply shows no gutter markers.
// An untracked file is wholly added; an unchanged file yields no markers.
func uncommittedChanges(ctx context.Context, abs string, lineCount int) []diffhunk.LineChange {
	dir := filepath.Dir(abs)
	status, err := gitOutput(ctx, dir, "status", "--porcelain=v1", "-z", "--", abs)
	if err != nil {
		return nil // not a git repo (or git unavailable) — no markers
	}
	record := status
	if idx := indexByteNUL(status); idx >= 0 {
		record = status[:idx]
	}
	if strings.TrimSpace(string(record)) == "" {
		return nil // unchanged
	}
	if len(record) >= 2 && record[0] == '?' && record[1] == '?' {
		if lineCount == 0 {
			return nil
		}
		return []diffhunk.LineChange{{Start: 1, End: lineCount, Kind: diffhunk.ChangeAdded}}
	}
	diff, err := gitOutput(ctx, dir, "diff", "HEAD", "--", abs)
	if err != nil {
		return nil //nolint:nilerr // no HEAD or diff failure degrades to no markers
	}
	return diffhunk.ChangedLines(string(diff))
}

func indexByteNUL(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return -1
}

// absRoot is the workspace as a plain absolute path — the SAME root
// ConfinedPath joins against, so a path it returns can be relativised back
// against it.
func absRoot(workspace string) string {
	if workspace == "" {
		return ""
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return workspace
	}
	return abs
}

// resolvedRoot is the workspace with symlinks resolved, for comparison against
// an already symlink-resolved absolute target. Both sides must be resolved or
// the comparison is lexical nonsense: on macOS a `/var` (or temp-dir) workspace
// path is itself a symlink to `/private/var`.
func resolvedRoot(workspace string) string {
	if workspace == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
		return resolved
	}
	return absRoot(workspace)
}

// relWithin returns absPath relative to root and whether it stays within root.
func relWithin(root, absPath string) (string, bool) {
	abs, err := filepath.Abs(absPath)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func isRegularFile(abs string) bool {
	info, err := os.Stat(abs)
	return err == nil && info.Mode().IsRegular()
}

// isBinary reports whether data looks like a binary blob (a NUL byte in the
// leading window), which the text viewer cannot render.
func isBinary(data []byte) bool {
	window := data
	if len(window) > 8000 {
		window = window[:8000]
	}
	for _, c := range window {
		if c == 0 {
			return true
		}
	}
	return false
}
