package github

// This file implements the GitHub side of the provider-neutral PR-retarget
// write contract (internal/observe/scm.PRRetargeter).
//
// Deliberate divergence: AO's two other GitHub writes go through GraphQL
// mutations (write.go), but this one uses REST. GitHub's GraphQL API exposes no
// clean mutation for changing a pull request's base, whereas
// `PATCH /repos/{owner}/{repo}/pulls/{n}` is the documented path. Using REST
// here is safe with respect to the client's ETag cache: doREST only caches GET
// (client.go, `cacheable := method == http.MethodGet`), so a PATCH cannot
// mis-replay a 304.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ scmobserve.PRRetargeter = (*Provider)(nil)

// BranchExists reports whether branch is present on the remote. It reuses the
// same single-branch endpoint BaseBranchGuard polls, but answers with a bool:
// a 404 means "no such branch", which is a normal answer the caller acts on
// rather than a failure to report upward.
func (p *Provider) BranchExists(ctx context.Context, repo ports.SCMRepo, branch string) (bool, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		// Guard before the request: an empty element would collapse the path to
		// the branches COLLECTION, which answers 200 and would be read as
		// "the branch exists".
		return false, nil
	}
	_, err := p.client.doRESTWithETag(ctx, repoPath(repo.Owner, repo.Name, "branches", branch), nil, "")
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, classifyWriteErr(err)
	}
	return true, nil
}

// RetargetPR points the pull request at target.
func (p *Provider) RetargetPR(ctx context.Context, ref ports.SCMPRRef, target string) error {
	path := repoPath(ref.Repo.Owner, ref.Repo.Name, "pulls", strconv.Itoa(ref.Number))
	_, err := p.client.doREST(ctx, http.MethodPatch, path, nil, map[string]string{"base": target})
	if err != nil {
		return classifyRetargetErr(err)
	}
	return nil
}

// classifyRetargetErr maps transport errors onto the provider-neutral write
// sentinels. 422 already arrives as ErrUnprocessable (== ports.ErrSCMInvalid)
// from classifyError, so it passes straight through; everything else follows
// the shared write classifier.
func classifyRetargetErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrUnprocessable) {
		return fmt.Errorf("github scm: retarget refused: %w", err)
	}
	return classifyWriteErr(err)
}
