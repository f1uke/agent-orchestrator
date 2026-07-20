package session

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/diffhunk"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// WorkspaceFileDiff returns one file's diff against the session's resolved
// target branch.
//
// This exists because DiffContext cannot serve Changes mode: it requires a
// prUrl that must match a PR already attributed to the session, and diffs
// pr.BaseSHA..pr.HeadSHA. A worker mid-task has no PR yet — which is precisely
// when the Files panel is most useful. Rather than widen DiffContext (whose
// contract is review-comment anchoring: mandatory line, hunk windowing), this
// shares only resolveTargetBranch with WorkspaceChanges and returns the same
// DiffContextResult shape so DiffRows/FileDiffView consume it unchanged.
//
// It also covers the case a naive implementation gets wrong: a DELETED file has
// no working-tree content, so reading it through the file endpoint 404s. Here it
// diffs correctly as an all-deletions patch.
func (s *Service) WorkspaceFileDiff(ctx context.Context, id domain.SessionID, relPath string) (DiffContextResult, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return DiffContextResult{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return DiffContextResult{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}

	workspace := rec.Metadata.WorkspacePath
	if workspace == "" || !isDir(workspace) {
		return DiffContextResult{Mode: "file", Path: relPath}, nil
	}
	// Confined deliberately — see confinedWorkspacePath on why this must not
	// reuse the terminal feature's absolute/`~` handling.
	abs, safePath, ok := confinedWorkspacePath(workspace, relPath)
	if !ok {
		return DiffContextResult{Mode: "file", Path: relPath}, nil
	}

	branch, _ := s.resolveTargetBranch(ctx, rec, workspace)
	if branch == "" {
		return DiffContextResult{Mode: "file", Path: safePath}, nil
	}
	ref, ok := resolveBranchRef(ctx, workspace, branch)
	if !ok {
		return DiffContextResult{Mode: "file", Path: safePath}, nil
	}
	baseOut, err := gitOutput(ctx, workspace, "merge-base", ref, "HEAD")
	if err != nil {
		return DiffContextResult{Mode: "file", Path: safePath}, nil //nolint:nilerr // intentional: degrade, don't error
	}
	mergeBase := strings.TrimSpace(string(baseOut))

	// No second ref: diff the merge base against the WORKING TREE, so a file the
	// worker has edited but not committed shows its real current state — the
	// same union WorkspaceChanges lists.
	out, err := gitOutput(ctx, workspace, "diff", "-M", mergeBase, "--", safePath)
	if err != nil {
		return DiffContextResult{Mode: "file", Path: safePath}, nil //nolint:nilerr // intentional: degrade, don't error
	}
	lines := diffhunk.AllLines(string(out))
	if len(lines) == 0 {
		// `git diff` never reports an UNTRACKED file, but WorkspaceChanges does
		// list one (a brand-new file a worker has not staged is exactly what a
		// reviewer wants to see). Without this the row would open on an empty
		// viewer. Synthesise the all-additions patch its content implies.
		if res, ok := untrackedAsAddedDiff(ctx, workspace, abs, safePath); ok {
			return res, nil
		}
		return DiffContextResult{Mode: "file", Path: safePath}, nil
	}

	res := DiffContextResult{Available: true, Mode: "file", Path: safePath}
	for i, l := range lines {
		if i >= maxFileLines {
			res.Truncated = true
			break
		}
		res.Lines = append(res.Lines, DiffContextLine{
			Kind: string(l.Kind), OldLine: l.OldLine, NewLine: l.NewLine, Text: l.Text,
		})
	}
	return res, nil
}

// untrackedAsAddedDiff renders an untracked file as the all-additions patch its
// content implies, so a brand-new file opens on real content instead of an empty
// viewer. Binary and oversized blobs are declined (ok=false) so the caller falls
// through to the normal "nothing to show" state.
func untrackedAsAddedDiff(ctx context.Context, workspace, abs, safePath string) (DiffContextResult, bool) {
	status, err := gitOutput(ctx, workspace, "status", "--porcelain=v1", "-z", "--", safePath)
	if err != nil {
		return DiffContextResult{}, false
	}
	rec := string(status)
	if idx := strings.IndexByte(rec, 0); idx >= 0 {
		rec = rec[:idx]
	}
	if !strings.HasPrefix(rec, "??") {
		return DiffContextResult{}, false
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxWorkspaceFileBytes {
		return DiffContextResult{}, false
	}
	data, err := os.ReadFile(abs) //nolint:gosec // path is workspace-confined by confinedWorkspacePath
	if err != nil || isBinary(data) {
		return DiffContextResult{}, false
	}
	res := DiffContextResult{Available: true, Mode: "file", Path: safePath}
	for i, row := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if i >= maxFileLines {
			res.Truncated = true
			break
		}
		res.Lines = append(res.Lines, DiffContextLine{Kind: "add", NewLine: i + 1, Text: row})
	}
	return res, len(res.Lines) > 0
}
