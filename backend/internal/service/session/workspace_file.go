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

// maxWorkspaceFileBytes guards against reading a huge/binary blob into memory
// before the line cap applies.
const maxWorkspaceFileBytes = 5 << 20 // 5 MiB

// WorkspaceFileResult is a workspace file's content plus the per-line map of its
// uncommitted changes (working tree vs HEAD).
type WorkspaceFileResult struct {
	Available    bool
	Path         string // repo-relative, slash-separated
	Lines        []DiffContextLine
	ChangedLines []diffhunk.LineChange
	Truncated    bool
}

// ResolveWorkspaceRef maps a file reference printed in the terminal (an absolute
// path, a workspace-relative path, or a bare filename) to candidate
// workspace-relative paths. All resolution is confined to the session's
// workspace: an absolute path pointing outside is never read, only its basename
// is searched for inside the workspace. Zero candidates (no match) is not an
// error; only an unknown session is.
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
	if workspace == "" || ref == "" {
		return nil, nil
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return nil, nil //nolint:nilerr // an unresolvable workspace yields no candidates, not an error
	}

	if filepath.IsAbs(ref) {
		// An absolute path inside the workspace resolves directly; one pointing
		// elsewhere is NEVER read — fall back to a basename search inside.
		if rel, within := relWithin(root, ref); within {
			if abs, ok := previewutil.ConfinedPath(workspace, rel); ok && isRegularFile(abs) {
				return []string{filepath.ToSlash(rel)}, nil
			}
		}
		return s.searchWorkspaceFiles(ctx, workspace, filepath.Base(ref), false), nil
	}

	if strings.ContainsAny(ref, "/\\") {
		clean := filepath.ToSlash(ref)
		if abs, ok := previewutil.ConfinedPath(workspace, clean); ok && isRegularFile(abs) {
			if rel, within := relWithin(root, abs); within {
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

// ReadWorkspaceFile reads a workspace file's content (confined to the session's
// workspace) and computes its per-line uncommitted-change map. A path escaping
// the workspace, or a missing/non-regular file, is a NotFound error; an unknown
// session is NotFound too. A non-git workspace yields content with no markers.
func (s *Service) ReadWorkspaceFile(ctx context.Context, id domain.SessionID, relPath string) (WorkspaceFileResult, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return WorkspaceFileResult{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return WorkspaceFileResult{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	workspace := rec.Metadata.WorkspacePath
	if workspace == "" {
		return WorkspaceFileResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "File not found in workspace")
	}
	abs, ok := previewutil.ConfinedPath(workspace, relPath)
	if !ok {
		return WorkspaceFileResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "File not found in workspace")
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return WorkspaceFileResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "File not found in workspace")
	}
	data, err := os.ReadFile(abs) //nolint:gosec // path is confined to the workspace by ConfinedPath
	if err != nil {
		return WorkspaceFileResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "File not found in workspace")
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return WorkspaceFileResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "File not found in workspace")
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return WorkspaceFileResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "File not found in workspace")
	}
	safePath := filepath.ToSlash(rel)

	if isBinary(data) || info.Size() > maxWorkspaceFileBytes {
		return WorkspaceFileResult{Available: false, Path: safePath}, nil
	}

	res := workspaceFileContent(safePath, string(data))
	res.ChangedLines = uncommittedChanges(ctx, workspace, safePath, len(res.Lines))
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
// HEAD for one file. An untracked file is wholly added; an unchanged file, or a
// non-git workspace, yields no markers.
func uncommittedChanges(ctx context.Context, workspace, safePath string, lineCount int) []diffhunk.LineChange {
	status, err := gitOutput(ctx, workspace, "status", "--porcelain=v1", "-z", "--", safePath)
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
	diff, err := gitOutput(ctx, workspace, "diff", "HEAD", "--", safePath)
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
