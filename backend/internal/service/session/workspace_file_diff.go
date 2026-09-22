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

// DiffBase selects which of the two kinds of "changed" a per-file diff answers.
// They are deliberately never merged into one: they answer different questions,
// and a reviewer needs both.
type DiffBase string

const (
	// DiffBaseTarget is merge-base(target, branch) .. WORKING TREE — everything
	// this branch did, committed or not. This is the level the Changes rail
	// lists, and the level the editor's branch gutter lane marks. When the
	// worktree is not standing on the session's branch it is
	// merge-base(target, branch) .. branch tip, exactly as the rail's list is.
	DiffBaseTarget DiffBase = "target"
	// DiffBaseHead is HEAD .. WORKING TREE — what Discard Change can undo, and
	// the level the editor's uncommitted gutter lane marks.
	DiffBaseHead DiffBase = "head"
)

// parseDiffBase normalises the wire value. An EMPTY base means target, so every
// caller written before the selector existed keeps asking the same question.
//
// 🗝 An unknown base is refused rather than falling back to the default. The two
// bases answer different questions, so quietly answering the other one is the
// same class of bug as ConfinedPath clamping `../x` to a file nobody named: the
// caller gets a confident answer to a question it did not ask.
func parseDiffBase(base DiffBase) (DiffBase, error) {
	switch base {
	case "", DiffBaseTarget:
		return DiffBaseTarget, nil
	case DiffBaseHead:
		return DiffBaseHead, nil
	default:
		return "", apierr.Invalid(
			"WORKSPACE_FILE_BASE_INVALID",
			fmt.Sprintf("Unknown diff base %q. Use %q or %q.", string(base), DiffBaseTarget, DiffBaseHead),
			map[string]any{"base": string(base)},
		)
	}
}

// FileDiffQuery selects one file's diff.
type FileDiffQuery struct {
	// Path is repo-relative. Absolute and ~/ paths are rejected.
	Path string
	// Base is which change level to answer; empty means DiffBaseTarget.
	Base DiffBase
	// FullContext includes every unchanged line rather than `git diff`'s three,
	// so a caller can replay EITHER SIDE of the file instead of only the hunks.
	//
	// 🗝 The windowed default is not a mere size optimisation and must stay the
	// default: the stacked diff view renders the skip markers that tell a reader
	// lines were left out, and those markers only exist because the payload is
	// windowed. Full context is for a caller that needs the file, not the diff -
	// the editor's Changes mode, which puts the original side in a diff editor.
	FullContext bool
}

// WorkspaceFileDiff returns one file's diff against the session's resolved
// target branch (base "target", the default) or against HEAD (base "head").
//
// This exists because DiffContext cannot serve Changes mode: it requires a
// prUrl that must match a PR already attributed to the session, and diffs
// pr.BaseSHA..pr.HeadSHA. A worker mid-task has no PR yet — which is precisely
// when the Files panel is most useful. Rather than widen DiffContext (whose
// contract is review-comment anchoring: mandatory line, hunk windowing), this
// shares resolveChangesScope with WorkspaceChanges and returns the same
// DiffContextResult shape so DiffRows/FileDiffView consume it unchanged.
//
// It also covers the case a naive implementation gets wrong: a DELETED file has
// no working-tree content, so reading it through the file endpoint 404s. Here it
// diffs correctly as an all-deletions patch.
func (s *Service) WorkspaceFileDiff(
	ctx context.Context, id domain.SessionID, q FileDiffQuery,
) (DiffContextResult, error) {
	base, err := parseDiffBase(q.Base)
	if err != nil {
		return DiffContextResult{}, err
	}
	relPath := q.Path
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

	// 🗝 The HEAD base resolves NO target branch and starts no background fetch.
	// Discard Change is a purely local gesture about this worktree and its last
	// commit; making it depend on a remote ref would tie an offline action to
	// the network, and would answer nothing at all for a session whose target
	// cannot be resolved.
	baseRev, headRev := "HEAD", ""
	if base == DiffBaseTarget {
		// The SAME scope the list resolved, not a second answer to the same
		// question: a row the list offered must open on the comparison the list
		// counted. Sharing it is what keeps a file listed as +38 from opening on
		// "no diff to show" because the worktree is parked on the base commit.
		//
		// The non-blocking target refresh rides along for the same reason - opening
		// a row must not show hunks measured against a staler target than the list
		// that offered it. Throttling is shared, so this is normally free.
		sc := s.resolveChangesScope(ctx, rec, workspace)
		if sc.Reason != "" {
			return DiffContextResult{Mode: "file", Path: safePath}, nil
		}
		baseRev = sc.MergeBase
		if !sc.IncludesWorktree {
			// The worktree stands on another baseline, so the branch's committed
			// tip is the right-hand side rather than the files on disk.
			headRev = sc.SubjectRev
		}
	}
	return diffFileAgainst(ctx, workspace, abs, safePath, baseRev, headRev, q.FullContext), nil
}

// diffFileAgainst renders one file's diff between baseRev and headRev. An EMPTY
// headRev means the WORKING TREE, so a file the worker has edited but not
// committed shows its real current state — the same union WorkspaceChanges lists
// while the worktree is on the session's branch. A named headRev is that branch's
// tip, used when the worktree is standing somewhere else entirely.
func diffFileAgainst(
	ctx context.Context, workspace, abs, safePath, baseRev, headRev string, fullContext bool,
) DiffContextResult {
	revs := []string{baseRev}
	if headRev != "" {
		revs = append(revs, headRev)
	}
	args := append([]string{"diff", "-M"}, revs...)
	if fullContext {
		// -U with the viewer's own line cap: any wider is wasted, and anything
		// this drops is dropped again by the maxFileLines truncation below.
		args = append([]string{"diff", "-M", fmt.Sprintf("-U%d", maxFileLines)}, revs...)
	}
	args = append(args, "--", safePath)
	out, err := gitOutput(ctx, workspace, args...)
	if err != nil {
		return DiffContextResult{Mode: "file", Path: safePath}
	}
	lines := diffhunk.AllLines(string(out))
	if len(lines) == 0 {
		// `git diff` never reports an UNTRACKED file, but WorkspaceChanges does
		// list one (a brand-new file a worker has not staged is exactly what a
		// reviewer wants to see). Without this the row would open on an empty
		// viewer. Synthesise the all-additions patch its content implies — it is
		// equally true against either base, since the file is in neither.
		if res, ok := untrackedAsAddedDiff(ctx, workspace, abs, safePath); ok {
			return res
		}
		return DiffContextResult{Mode: "file", Path: safePath}
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
	return res
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
