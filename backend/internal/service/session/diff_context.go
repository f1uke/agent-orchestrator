package session

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/diffhunk"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const maxFileLines = 2000

// hunkContextWindow bounds how many hunk lines the "hunk" mode returns around a
// review comment's anchor, so a large hunk doesn't dump the whole file into the
// comment card. The full file remains reachable via mode=file ("Expand full
// file"). Odd so the anchor sits centered with equal context above and below.
const hunkContextWindow = 15

// DiffContextLine is one classified line of returned code context.
type DiffContextLine struct {
	Kind    string // "context" | "add" | "del"
	OldLine int
	NewLine int
	Text    string
}

// DiffContextResult is the code context for a review comment anchor.
type DiffContextResult struct {
	Available bool
	Mode      string
	Path      string
	Lines     []DiffContextLine
	Truncated bool
}

// DiffContextQuery selects the code context to return.
type DiffContextQuery struct {
	PRURL string
	Path  string
	Line  int
	Mode  string // "hunk" (default) or "file"
}

// gitOutput runs git in dir and returns stdout. Overridable in tests.
var gitOutput = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd.Output()
}

// DiffContext returns the diff hunk (or whole file) the review comment anchors
// to, read from the session's git worktree. Unavailable context (missing SHA,
// git failure, path outside the repo, or no hunk covering the line) is reported
// as Available:false rather than an error, so the UI can degrade gracefully.
// Unknown session or a PR URL that doesn't belong to the session are the only
// error cases — both surface as apierr.NotFound.
func (s *Service) DiffContext(ctx context.Context, id domain.SessionID, q DiffContextQuery) (DiffContextResult, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return DiffContextResult{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return DiffContextResult{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	workspace := rec.Metadata.WorkspacePath

	prs, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return DiffContextResult{}, err
	}
	var pr domain.PullRequest
	found := false
	for _, p := range prs {
		if p.URL == q.PRURL {
			pr, found = p, true
			break
		}
	}
	if !found {
		return DiffContextResult{}, apierr.NotFound("PR_NOT_FOUND", "Unknown PR for session")
	}

	mode := q.Mode
	if mode == "" {
		mode = "hunk"
	}

	if workspace == "" {
		return DiffContextResult{Available: false, Mode: mode, Path: q.Path}, nil
	}
	abs, ok := previewutil.ConfinedPath(workspace, q.Path)
	if !ok {
		return DiffContextResult{Available: false, Mode: mode, Path: q.Path}, nil
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return DiffContextResult{Available: false, Mode: mode, Path: q.Path}, nil //nolint:nilerr // intentional: an unresolvable path degrades to Available:false, not an error
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return DiffContextResult{Available: false, Mode: mode, Path: q.Path}, nil //nolint:nilerr // intentional: an unresolvable path degrades to Available:false, not an error
	}
	safePath := filepath.ToSlash(rel)

	headRef := pr.HeadSHA
	if strings.TrimSpace(headRef) == "" {
		headRef = "HEAD"
	}

	if mode == "file" {
		out, err := gitOutput(ctx, workspace, "show", headRef+":"+safePath)
		if err != nil {
			return DiffContextResult{Available: false, Mode: "file", Path: q.Path}, nil //nolint:nilerr // intentional: an unreadable file degrades to Available:false, not an error
		}
		return fileResult(q.Path, string(out)), nil
	}

	// hunk mode
	if strings.TrimSpace(pr.BaseSHA) == "" {
		return DiffContextResult{Available: false, Mode: "hunk", Path: q.Path}, nil
	}
	out, err := gitOutput(ctx, workspace, "diff", pr.BaseSHA+".."+headRef, "--", safePath)
	if err != nil {
		return DiffContextResult{Available: false, Mode: "hunk", Path: q.Path}, nil //nolint:nilerr // intentional: an unreadable diff degrades to Available:false, not an error
	}
	lines, hit := diffhunk.HunkForLine(string(out), q.Line)
	if !hit {
		return DiffContextResult{Available: false, Mode: "hunk", Path: q.Path}, nil
	}
	windowed, trimmed := windowAroundLine(lines, q.Line)
	res := DiffContextResult{Available: true, Mode: "hunk", Path: q.Path, Truncated: trimmed}
	for _, l := range windowed {
		res.Lines = append(res.Lines, DiffContextLine{Kind: string(l.Kind), OldLine: l.OldLine, NewLine: l.NewLine, Text: l.Text})
	}
	return res, nil
}

// windowAroundLine trims a hunk to at most hunkContextWindow lines centered on
// the line whose new-side number is target, clamped at the hunk's edges. A large
// hunk — e.g. a newly added file rendered as one big hunk — would otherwise dump
// the whole file into the comment; the full file stays available via mode=file.
// trimmed reports whether any lines were dropped.
func windowAroundLine(lines []diffhunk.Line, target int) ([]diffhunk.Line, bool) {
	if len(lines) <= hunkContextWindow {
		return lines, false
	}
	anchor := 0
	for i, l := range lines {
		if l.NewLine == target {
			anchor = i
			break
		}
	}
	start := anchor - hunkContextWindow/2
	if start < 0 {
		start = 0
	}
	end := start + hunkContextWindow
	if end > len(lines) {
		end = len(lines)
		start = end - hunkContextWindow
	}
	return lines[start:end], true
}

// fileResult numbers a whole-file `git show` blob as context lines, capping at
// maxFileLines.
func fileResult(path, content string) DiffContextResult {
	rows := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	res := DiffContextResult{Available: true, Mode: "file", Path: path}
	for i, r := range rows {
		if i >= maxFileLines {
			res.Truncated = true
			break
		}
		res.Lines = append(res.Lines, DiffContextLine{Kind: "context", NewLine: i + 1, Text: r})
	}
	return res
}
