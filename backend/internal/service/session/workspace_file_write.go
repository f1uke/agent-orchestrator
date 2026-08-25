package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/diffhunk"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Reasons submitted content is refused. They mirror the read path's
// unavailability reasons, because they ask the same question from the other
// side: could the viewer show these bytes back to whoever just saved them?
const (
	ContentTooLarge     = "too_large"
	ContentTooManyLines = "too_many_lines"
	ContentBinary       = "binary"
)

// WriteWorkspaceFileInput is one save: the file, its new content, and the
// precondition that says which bytes the caller was editing.
type WriteWorkspaceFileInput struct {
	// Path is workspace-relative and slash-separated. Absolute and `~/` paths
	// are REFUSED here even though ReadWorkspaceFile accepts them; see
	// WriteWorkspaceFile.
	Path string
	// Content is written verbatim - no EOL translation, no trailing-newline
	// insertion. Whitespace policy belongs to the editor, which can show the
	// user what it did; a server that silently rewrites bytes cannot.
	Content string
	// BaseHash is the ContentHash the read handed out. It is REQUIRED: there is
	// deliberately no way to spell "write regardless".
	BaseHash string
}

// WriteWorkspaceFileResult is what the caller needs to keep editing without a
// second round trip: the new precondition token and the refreshed gutter map.
type WriteWorkspaceFileResult struct {
	// Path is the workspace-relative path actually written.
	Path string
	// ContentHash is the hash of the bytes now on disk - the BaseHash for the
	// caller's next save.
	ContentHash string
	Size        int
	// ChangedLines is the uncommitted-change map recomputed after the write, so
	// the gutter updates without a follow-up read that could race an agent.
	ChangedLines []diffhunk.LineChange
}

// WriteWorkspaceFile replaces one workspace file's content.
//
// It is deliberately narrower than ReadWorkspaceFile in three ways, each of
// which is load-bearing:
//
//   - CONFINED. The read path is intentionally unconfined for absolute and `~/`
//     paths (#132) so a knowledge-store note can be opened in the viewer. That
//     decision does not carry over: reading anywhere on disk through a no-auth
//     loopback daemon is a viewer convenience, writing anywhere on disk is an
//     escalation. A path that is not inside this session's workspace is
//     refused, including one that only leaves it after its symlinks resolve.
//     The consequence for the editor is that a file outside the workspace opens
//     read-only.
//   - EXISTING FILES ONLY. Save-what-you-opened is the whole use case, and
//     "must already exist" removes directory creation and the symlinked-parent
//     hole that cannot be checked for a path that is not there yet.
//   - PRECONDITIONED. An AO worktree has agents writing in it, so "the file
//     changed since you opened it" is the normal case. The caller must hand back
//     the ContentHash it read; a mismatch is a conflict, never a clobber.
//
// It also refuses to write a file the caller could not have been holding in
// full - one the read truncated, or that is now binary or over the size cap.
// That verdict is re-derived here from the file on disk: a client-side guard is
// not a guard, because a client that forgot is exactly the failure being
// guarded against.
func (s *Service) WriteWorkspaceFile(ctx context.Context, id domain.SessionID, in WriteWorkspaceFileInput) (WriteWorkspaceFileResult, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return WriteWorkspaceFileResult{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return WriteWorkspaceFileResult{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	workspace := rec.Metadata.WorkspacePath
	if workspace == "" {
		return WriteWorkspaceFileResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "Session has no workspace on disk")
	}
	target, rel, ok := writableWorkspacePath(workspace, in.Path)
	if !ok {
		return WriteWorkspaceFileResult{}, apierr.Invalid(
			"WORKSPACE_FILE_PATH_INVALID",
			"Path must name a file inside the session's workspace",
			nil,
		)
	}
	if strings.TrimSpace(in.BaseHash) == "" {
		return WriteWorkspaceFileResult{}, apierr.Invalid(
			"WORKSPACE_FILE_BASE_HASH_REQUIRED",
			"Saving requires the contentHash the file was read with",
			nil,
		)
	}

	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return WriteWorkspaceFileResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "File not found in workspace")
	}
	// Over the cap the read returned no content and no hash at all, so there is
	// no precondition to compare - and hashing it would pull a blob of any size
	// into memory, which is the very thing the read-side cap exists to prevent.
	// The same holds for a binary file below: reporting a conflict for a file
	// the caller was never given a hash for would name the wrong problem.
	if info.Size() > maxWorkspaceFileBytes {
		return WriteWorkspaceFileResult{}, notEditable(UnavailableTooLarge)
	}
	current, err := os.ReadFile(target) //nolint:gosec // target is confined to the session's workspace above
	if err != nil {
		return WriteWorkspaceFileResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "File not found in workspace")
	}
	if isBinary(current) {
		return WriteWorkspaceFileResult{}, notEditable(UnavailableBinary)
	}
	if got := contentHash(current); got != in.BaseHash {
		return WriteWorkspaceFileResult{}, apierr.Conflict(
			"WORKSPACE_FILE_CONFLICT",
			"The file changed on disk since it was read",
			map[string]any{
				"currentHash":       got,
				"currentSize":       len(current),
				"currentModifiedAt": info.ModTime().UTC().Format(time.RFC3339Nano),
			},
		)
	}
	// The precondition passed, so these bytes ARE what the caller was shown -
	// and a file this long was shown only down to maxFileLines, so saving back
	// what the caller holds would destroy everything past that line. Unlike the
	// two checks above, the read DOES hand out a hash for a truncated file, so
	// this one has to come after the precondition: "it changed underneath you"
	// is the more actionable answer when both are true.
	if lineCount(string(current)) > maxFileLines {
		return WriteWorkspaceFileResult{}, notEditable(UnavailableTruncated)
	}
	if err := checkWritableContent(in.Content); err != nil {
		return WriteWorkspaceFileResult{}, err
	}

	data := []byte(in.Content)
	if err := writeFileAtomic(target, data, info.Mode().Perm()); err != nil {
		return WriteWorkspaceFileResult{}, apierr.Internal("WORKSPACE_FILE_WRITE_FAILED", "Could not write the file")
	}
	return WriteWorkspaceFileResult{
		Path:         rel,
		ContentHash:  contentHash(data),
		Size:         len(data),
		ChangedLines: uncommittedChanges(ctx, target, lineCount(in.Content)),
	}, nil
}

// notEditable names a file the editor cannot round-trip. It is a CONFLICT
// rather than an invalid request: the request is well formed, the file's state
// is what makes it unsafe, and that state can change.
func notEditable(reason string) error {
	return apierr.Conflict(
		"WORKSPACE_FILE_NOT_EDITABLE",
		"The file cannot be saved from the editor: "+reason,
		map[string]any{"reason": reason},
	)
}

// checkWritableContent refuses content the viewer could not show back in full.
// Letting a save push a file past the line cap would leave a file the editor
// can only display in part and, by the truncation rule above, can never save
// again - a dead end it is better to refuse than to create.
func checkWritableContent(content string) error {
	var reason string
	switch {
	case len(content) > maxWorkspaceFileBytes:
		reason = ContentTooLarge
	case lineCount(content) > maxFileLines:
		reason = ContentTooManyLines
	case isBinary([]byte(content)):
		reason = ContentBinary
	default:
		return nil
	}
	return apierr.Invalid(
		"WORKSPACE_FILE_CONTENT_REJECTED",
		"The content cannot be stored by the editor: "+reason,
		map[string]any{
			"reason":     reason,
			"limitBytes": maxWorkspaceFileBytes,
			"limitLines": maxFileLines,
		},
	)
}

// lineCount counts the lines the viewer would render for content, using the
// same split as workspaceFileContent so "would this read back truncated?" is
// answered by the same arithmetic that truncates it.
func lineCount(content string) int {
	return len(strings.Split(strings.TrimSuffix(content, "\n"), "\n"))
}

// contentHash identifies a file by its exact bytes. The algorithm is in the
// value, so the token stays self-describing if it ever changes.
func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// writableWorkspacePath validates a save target and returns the path to write
// plus the workspace-relative path to report.
//
// Confinement is confinedWorkspacePath - the same hardened check the rest of
// this package uses - with a `..` rejection in front of it. That is not
// belt-and-braces: previewutil.ConfinedPath CLAMPS traversal instead of
// rejecting it, so "../a.go" arrives as "<workspace>/a.go". The result stays
// inside the workspace, so it is not a containment hole, but it names a
// DIFFERENT file than the caller asked for - and quietly writing over the wrong
// file is data loss, not a cosmetic mismatch.
//
// The returned path is symlink-RESOLVED. A symlink inside the workspace is a
// legitimate target and the write must go through it; renaming over the link
// itself would replace it with a regular file and silently detach it from what
// it pointed at.
func writableWorkspacePath(workspace, p string) (target, rel string, ok bool) {
	if hasParentSegment(p) {
		return "", "", false
	}
	// A path that cleans away to nothing ("/", "\\", "//") is rejected here
	// rather than passed on, because ConfinedPath REWRITES that case to
	// "index.html" - a leftover from serving a preview directory. On Unix the
	// absolute check below already catches "/", but filepath.IsAbs("/") is
	// false on Windows, so without this the route would silently target a file
	// nobody named.
	if strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(p)), "/") == "" {
		return "", "", false
	}
	confined, safeRel, ok := confinedWorkspacePath(workspace, p)
	if !ok {
		return "", "", false
	}
	resolved, err := filepath.EvalSymlinks(confined)
	if err != nil {
		// Nothing there to resolve. confinedWorkspacePath already established
		// the lexical path is inside the workspace, so this is a missing file,
		// which the caller reports as such.
		return confined, safeRel, true
	}
	if _, within := relWithin(resolvedRoot(workspace), resolved); !within {
		return "", "", false
	}
	return resolved, safeRel, true
}

// writeFileAtomic replaces a file's contents through a same-directory temp file
// and a rename, so a concurrent reader - an agent, a build - never observes a
// half-written file. The original's permission bits are carried over.
//
// The rename gives the file a new inode, which breaks any hard link to it. That
// is the accepted cost of never leaving a torn file behind.
func writeFileAtomic(target string, data []byte, mode os.FileMode) error {
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
