package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/diffhunk"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const maxFileLines = 2000

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
	if _, ok := previewutil.ConfinedPath(workspace, q.Path); !ok {
		return DiffContextResult{Available: false, Mode: mode, Path: q.Path}, nil
	}

	headRef := pr.HeadSHA
	if strings.TrimSpace(headRef) == "" {
		headRef = "HEAD"
	}

	if mode == "file" {
		out, err := gitOutput(ctx, workspace, "show", headRef+":"+q.Path)
		if err != nil {
			return DiffContextResult{Available: false, Mode: "file", Path: q.Path}, nil
		}
		return fileResult(q.Path, string(out)), nil
	}

	// hunk mode
	if strings.TrimSpace(pr.BaseSHA) == "" {
		return DiffContextResult{Available: false, Mode: "hunk", Path: q.Path}, nil
	}
	out, err := gitOutput(ctx, workspace, "diff", pr.BaseSHA+".."+headRef, "--", q.Path)
	if err != nil {
		return DiffContextResult{Available: false, Mode: "hunk", Path: q.Path}, nil
	}
	lines, hit := diffhunk.HunkForLine(string(out), q.Line)
	if !hit {
		return DiffContextResult{Available: false, Mode: "hunk", Path: q.Path}, nil
	}
	res := DiffContextResult{Available: true, Mode: "hunk", Path: q.Path}
	for _, l := range lines {
		res.Lines = append(res.Lines, DiffContextLine{Kind: string(l.Kind), OldLine: l.OldLine, NewLine: l.NewLine, Text: l.Text})
	}
	return res, nil
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
