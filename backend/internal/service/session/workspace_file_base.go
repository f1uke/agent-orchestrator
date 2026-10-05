package session

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Reasons a file's base text cannot be offered. The text-viewer reasons
// (UnavailableTooLarge, UnavailableBinary, UnavailableSubmodule,
// UnavailableDirectory) are reused for the same verdicts about the base blob.
const (
	// BaseUnavailableNoRepo: the workspace is missing or is not a git work tree.
	BaseUnavailableNoRepo = "no_repo"
	// BaseUnavailableNoTarget: the target base was asked for and the session's
	// target branch, or its merge-base with the branch, cannot be resolved.
	BaseUnavailableNoTarget = "no_target"
	// BaseUnavailableNotOnBranch: the worktree is not standing on the session's
	// branch, so the branch's changes are measured to the branch TIP, not to the
	// files on disk - and a buffer of those files cannot be compared to the base.
	BaseUnavailableNotOnBranch = "not_on_branch"
	// BaseUnavailableSymlink: the path is a symlink at the base. Its blob is the
	// link target's NAME, while the viewer reads what the link points at.
	BaseUnavailableSymlink = "symlink"
)

// FileBaseQuery selects which revision of one file to read.
type FileBaseQuery struct {
	// Path is repo-relative. Absolute and ~/ paths are rejected.
	Path string
	// Base is which change level's base to read; empty means DiffBaseTarget.
	Base DiffBase
}

// WorkspaceFileBaseResult is one file's text at the base of a change level.
type WorkspaceFileBaseResult struct {
	// Available is false when no base can be offered; Reason says why.
	Available bool
	Reason    string
	Path      string
	// Revision is the commit the text was read from. Empty when Available is
	// false, and on an unborn HEAD (there is no commit to name).
	Revision string
	// Exists is false when the file is not in that revision - it is new on this
	// branch, or untracked - so every line of it is an addition.
	Exists bool
	// Text is the file's content at Revision, in its WORKING-TREE form (eol and
	// smudge filters applied), so it compares byte for byte with what is on disk.
	Text string
}

// WorkspaceFileBase returns one file's text at the base of a change level:
// HEAD for "head", merge-base(target, branch) for "target".
//
// It exists so the editor can measure its LIVE BUFFER against git. The per-file
// diff answers "what changed on disk", which goes stale on the first keystroke
// and is capped at maxFileLines diff rows; the base text answers every
// keystroke after it, at any size the viewer opens, with the comparison done
// where the buffer is.
//
// 🗝 The target base is offered only while the worktree stands on the session's
// branch. Parked elsewhere, the branch's changes are measured to its committed
// tip rather than to the files on disk (see resolveChangesScope), and a base
// compared against a buffer of DIFFERENT files would report confident nonsense.
func (s *Service) WorkspaceFileBase(
	ctx context.Context, id domain.SessionID, q FileBaseQuery,
) (WorkspaceFileBaseResult, error) {
	base, err := parseDiffBase(q.Base)
	if err != nil {
		return WorkspaceFileBaseResult{}, err
	}
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return WorkspaceFileBaseResult{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return WorkspaceFileBaseResult{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}

	workspace := rec.Metadata.WorkspacePath
	if workspace == "" || !isDir(workspace) {
		return WorkspaceFileBaseResult{Path: q.Path, Reason: BaseUnavailableNoRepo}, nil
	}
	// Confined deliberately, as the per-file diff is: git objects are read for a
	// path inside this worktree only.
	_, safePath, ok := confinedWorkspacePath(workspace, q.Path)
	if !ok {
		return WorkspaceFileBaseResult{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "File not found in workspace")
	}
	unavailable := func(reason string) (WorkspaceFileBaseResult, error) {
		return WorkspaceFileBaseResult{Path: safePath, Reason: reason}, nil
	}

	rev := "HEAD"
	if base == DiffBaseTarget {
		sc := s.resolveChangesScope(ctx, rec, workspace, false)
		if sc.Reason != "" {
			return unavailable(BaseUnavailableNoTarget)
		}
		if !sc.IncludesWorktree {
			return unavailable(BaseUnavailableNotOnBranch)
		}
		rev = sc.MergeBase
	}

	commitOut, err := gitOutput(ctx, workspace, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		// An unborn HEAD in a real work tree has no commit yet, so every file in
		// it is new. Anything else is not a repository we can read.
		if base == DiffBaseHead && insideWorkTree(ctx, workspace) {
			return WorkspaceFileBaseResult{Available: true, Path: safePath}, nil
		}
		return unavailable(BaseUnavailableNoRepo)
	}
	commit := strings.TrimSpace(string(commitOut))

	// ls-tree first: it tells a file that is simply not in this revision (new,
	// untracked - all of it is added) apart from a git failure, which a bare
	// `cat-file` exit code cannot. Paths are cwd-relative for ls-tree, and the
	// workspace is the cwd.
	entry, err := gitOutput(ctx, workspace, "ls-tree", "-z", commit, "--", safePath)
	if err != nil {
		return unavailable(BaseUnavailableNoRepo)
	}
	mode, kind, object, found := parseLsTreeEntry(entry)
	if !found {
		return WorkspaceFileBaseResult{Available: true, Path: safePath, Revision: commit}, nil
	}
	switch {
	case kind == "commit":
		return unavailable(UnavailableSubmodule)
	case kind == "tree":
		return unavailable(UnavailableDirectory)
	case kind != "blob":
		return unavailable(BaseUnavailableNoRepo)
	case mode == "120000":
		return unavailable(BaseUnavailableSymlink)
	}

	// The size is checked BEFORE the blob is read, as the file reader checks the
	// stat size, so a huge blob is never pulled into memory.
	sizeOut, err := gitOutput(ctx, workspace, "cat-file", "-s", object)
	if err != nil {
		return unavailable(BaseUnavailableNoRepo)
	}
	if size, err := strconv.ParseInt(strings.TrimSpace(string(sizeOut)), 10, 64); err != nil || size > maxWorkspaceFileBytes {
		return unavailable(UnavailableTooLarge)
	}
	// --filters applies the same eol conversion and smudge filters a checkout
	// would, so a CRLF or filtered file is not reported as changed on every line.
	// `./` makes the path cwd-relative, matching ls-tree above.
	data, err := gitOutput(ctx, workspace, "cat-file", "--filters", commit+":./"+safePath)
	if err != nil {
		return unavailable(BaseUnavailableNoRepo)
	}
	if isBinary(data) {
		return unavailable(UnavailableBinary)
	}
	return WorkspaceFileBaseResult{Available: true, Path: safePath, Revision: commit, Exists: true, Text: string(data)}, nil
}

// parseLsTreeEntry reads the first record of `git ls-tree -z`:
// "<mode> SP <type> SP <object> TAB <path> NUL".
func parseLsTreeEntry(out []byte) (mode, kind, object string, ok bool) {
	record := string(out)
	if idx := strings.IndexByte(record, 0); idx >= 0 {
		record = record[:idx]
	}
	meta, _, hasTab := strings.Cut(record, "\t")
	if !hasTab {
		return "", "", "", false
	}
	fields := strings.Fields(meta)
	if len(fields) != 3 {
		return "", "", "", false
	}
	return fields[0], fields[1], fields[2], true
}

func insideWorkTree(ctx context.Context, dir string) bool {
	out, err := gitOutput(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(string(out)) == "true"
}
