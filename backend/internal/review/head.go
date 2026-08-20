package review

import (
	"context"
	"fmt"
	"strings"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Head resolves what a checkout is, for a review that has no PR to key on. A
// post-MR pass takes its branch and head commit from the observed PR row; a
// pre-MR pass has no such row, so it must read the worktree itself.
//
// A detached HEAD, an unborn branch, or a path that is not a repo yields an
// empty branch/sha and no error: Trigger turns that into an ErrInvalid the user
// can act on, rather than a 500.
type Head interface {
	Head(ctx context.Context, workspacePath string) (branch, sha string, err error)
}

// gitHead is the production Head: two plumbing reads against the worker's own
// checkout. It is deliberately read-only — the reviewer floor forbids the review
// path from touching the branch, and so does this.
type gitHead struct{}

// NewGitHead returns the production Head resolver.
func NewGitHead() Head { return gitHead{} }

func (gitHead) Head(ctx context.Context, workspacePath string) (string, string, error) {
	if strings.TrimSpace(workspacePath) == "" {
		return "", "", nil
	}
	// --abbrev-ref HEAD prints "HEAD" on a detached checkout; treat that as "no
	// branch" rather than inventing one, so the caller refuses instead of keying
	// a durable run on a name that means nothing.
	branch, err := gitLine(ctx, workspacePath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || branch == "HEAD" {
		return "", "", nil //nolint:nilerr // intentional: an unreadable checkout is "nothing to review", not an error
	}
	sha, err := gitLine(ctx, workspacePath, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", "", nil //nolint:nilerr // intentional: an unborn HEAD is "nothing to review", not an error
	}
	return branch, sha, nil
}

func gitLine(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := aoprocess.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git -C %s %s: %w", dir, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}
