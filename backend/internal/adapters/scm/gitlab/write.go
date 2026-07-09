package gitlab

// This file implements the GitLab side of the provider-neutral review-thread
// write contract (internal/observe/scm.ReviewThreadWriter): replying to and
// resolving a merge-request discussion via GitLab's REST v4 API.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Provider satisfies the review-thread write capability. The github and
// composite providers declare the same assertion in production code, so this
// mirrors the codebase convention (no import cycle: observe/scm defines the
// interfaces and never imports the concrete adapters).
var _ scmobserve.ReviewThreadWriter = (*Provider)(nil)

// ReplyToThread posts body as a note on the given merge-request discussion
// and returns the normalized comment GitLab created.
func (p *Provider) ReplyToThread(ctx context.Context, ref ports.SCMPRRef, threadID, body string) (ports.SCMReviewCommentObservation, error) {
	path := "projects/" + projectID(ref.Repo) + "/merge_requests/" + strconv.Itoa(ref.Number) + "/discussions/" + url.PathEscape(threadID) + "/notes"
	resp, err := p.client.doRESTWithETagAndMethod(ctx, http.MethodPost, path, nil, "", map[string]string{"body": body})
	if err != nil {
		return ports.SCMReviewCommentObservation{}, classifyGitlabWriteErr(resp, err)
	}
	var n restNote
	if err := json.Unmarshal(resp.Body, &n); err != nil {
		return ports.SCMReviewCommentObservation{}, fmt.Errorf("gitlab scm: decode reply note: %w", err)
	}
	return ports.SCMReviewCommentObservation{
		ID:     strconv.Itoa(n.ID),
		Author: n.Author.Username,
		Body:   n.Body,
		IsBot:  isBotUsername(n.Author.Username),
	}, nil
}

// ResolveThread marks the given merge-request discussion resolved.
func (p *Provider) ResolveThread(ctx context.Context, ref ports.SCMPRRef, threadID string) error {
	path := "projects/" + projectID(ref.Repo) + "/merge_requests/" + strconv.Itoa(ref.Number) + "/discussions/" + url.PathEscape(threadID)
	q := url.Values{"resolved": {"true"}}
	resp, err := p.client.doRESTWithETagAndMethod(ctx, http.MethodPut, path, q, "", nil)
	if err != nil {
		return classifyGitlabWriteErr(resp, err)
	}
	return nil
}

// classifyGitlabWriteErr maps client transport errors onto the
// provider-neutral write sentinels. GitLab's classifyError (client.go:199)
// maps both 401 and 403 onto ErrAuthFailed, which becomes
// ports.ErrSCMForbidden here; unlike GitHub, GitLab's classifyError does not
// give 404 a typed sentinel (client.go:199-205), so a 404 status is
// translated explicitly by checking resp.Status.
func classifyGitlabWriteErr(resp restResponse, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrAuthFailed) {
		return fmt.Errorf("%w: %w", ports.ErrSCMForbidden, err)
	}
	if resp.Status == http.StatusNotFound {
		return fmt.Errorf("%w: %w", ports.ErrSCMNotFound, err)
	}
	return err
}
