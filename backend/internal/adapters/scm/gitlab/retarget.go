package gitlab

// This file implements the GitLab side of the provider-neutral PR-retarget
// write contract (internal/observe/scm.PRRetargeter): pointing an open merge
// request at a different target branch via GitLab's REST v4 API.

import (
	"context"

	"fmt"
	"net/http"
	"net/url"
	"strconv"

	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ scmobserve.PRRetargeter = (*Provider)(nil)

// BranchExists reports whether branch is present on the remote. It reuses the
// same single-branch endpoint BaseBranchGuard polls, but answers with a bool:
// a 404 here means "no such branch", which is a normal answer the caller acts
// on, not a failure to report upward.
func (p *Provider) BranchExists(ctx context.Context, repo ports.SCMRepo, branch string) (bool, error) {
	path := "projects/" + projectID(repo) + "/repository/branches/" + url.PathEscape(branch)
	resp, err := p.client.doRESTWithETag(ctx, path, nil, "")
	if err != nil {
		if resp.Status == http.StatusNotFound {
			return false, nil
		}
		return false, classifyGitlabWriteErr(resp, err)
	}
	return true, nil
}

// RetargetPR points the merge request at target.
func (p *Provider) RetargetPR(ctx context.Context, ref ports.SCMPRRef, target string) error {
	path := "projects/" + projectID(ref.Repo) + "/merge_requests/" + strconv.Itoa(ref.Number)
	resp, err := p.client.doRESTWithETagAndMethod(ctx, http.MethodPut, path, nil, "",
		map[string]string{"target_branch": target})
	if err != nil {
		return classifyGitlabRetargetErr(resp, err)
	}
	return nil
}

// classifyGitlabRetargetErr extends the shared write classifier with the 400/422
// case: GitLab uses them when it refuses a change on its merits (target equal to
// the source, merge request already merged), which is a statement about the
// REQUEST, not the service. Without this the error reaches the human as a 503
// "SCM unavailable" and tells them to retry something that can never succeed.
//
// ⚠ It does NOT cover a target branch that does not exist. Verified against a
// real GitLab instance (finnomena, MR !3041): a PUT naming a nonexistent
// target_branch returns **200** and GitLab silently points the merge request at
// the missing branch. GitHub refuses the same request with 422. That asymmetry
// is why BranchExists is checked BEFORE the write in the service layer rather
// than inferring the outcome from the response — on GitLab the pre-flight is the
// ONLY thing standing between a typo and a merge request aimed at nothing.
func classifyGitlabRetargetErr(resp restResponse, err error) error {
	if err == nil {
		return nil
	}
	if resp.Status == http.StatusBadRequest || resp.Status == http.StatusUnprocessableEntity {
		return fmt.Errorf("%w: %w", ports.ErrSCMInvalid, err)
	}
	return classifyGitlabWriteErr(resp, err)
}
