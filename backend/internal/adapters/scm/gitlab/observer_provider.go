package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ParseRepository normalizes a GitLab remote/origin URL into a
// provider-neutral repository key. It accepts SSH
// (git@host:group/sub/proj.git) and HTTPS
// (https://host/group/sub/proj(.git)) forms. GitLab supports arbitrarily
// nested groups, so the full path (minus the trailing ".git") becomes
// Repo, the last path segment becomes Name, and everything before it
// becomes Owner (e.g. "group/sub/proj" -> Owner "group/sub", Name
// "proj"). Remotes whose host does not match this provider's configured
// Host return ok=false so a composite dispatcher can try the next SCM
// provider instead of misclaiming the remote.
func (p *Provider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	raw := strings.TrimSpace(remote)
	if raw == "" {
		return ports.SCMRepo{}, false
	}

	var host, pathPart string
	if strings.HasPrefix(raw, "git@") {
		rest := strings.TrimPrefix(raw, "git@")
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) != 2 {
			return ports.SCMRepo{}, false
		}
		host = parts[0]
		pathPart = parts[1]
	} else {
		u, err := url.Parse(raw)
		if err != nil {
			return ports.SCMRepo{}, false
		}
		host = u.Host
		pathPart = u.Path
	}

	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || !strings.EqualFold(host, p.host) {
		return ports.SCMRepo{}, false
	}

	pathPart = strings.Trim(pathPart, "/")
	pathPart = strings.TrimSuffix(pathPart, ".git")
	if pathPart == "" {
		return ports.SCMRepo{}, false
	}

	segments := strings.Split(pathPart, "/")
	if len(segments) < 2 {
		return ports.SCMRepo{}, false
	}
	name := segments[len(segments)-1]
	owner := strings.Join(segments[:len(segments)-1], "/")
	if owner == "" || name == "" {
		return ports.SCMRepo{}, false
	}

	return ports.SCMRepo{
		Provider: "gitlab",
		Host:     host,
		Owner:    owner,
		Name:     name,
		Repo:     pathPart,
	}, true
}

// projectID returns the URL-escaped GitLab project path used as the REST
// v4 `:id` path segment (GitLab accepts either the numeric project ID or
// the URL-encoded namespace/path, e.g. "group%2Fsub%2Fproj").
func projectID(repo ports.SCMRepo) string {
	return url.PathEscape(repo.Repo)
}

// mrListPath is the shared merge-requests list endpoint used by both the
// full list fetch and the cheap ETag guard.
func mrListPath(repo ports.SCMRepo) string {
	return "projects/" + projectID(repo) + "/merge_requests"
}

// pipelinesPath is the shared per-commit pipelines endpoint used by
// CommitChecksGuard.
func pipelinesPath(repo ports.SCMRepo) string {
	return "projects/" + projectID(repo) + "/pipelines"
}

// ListOpenPRsByRepo lists every open merge request in the project so the
// observer can attribute each to a session by source-branch prefix.
func (p *Provider) ListOpenPRsByRepo(ctx context.Context, repo ports.SCMRepo) ([]ports.SCMPRObservation, error) {
	q := url.Values{}
	q.Set("state", "opened")
	q.Set("per_page", "100")
	resp, err := p.client.doREST(ctx, http.MethodGet, mrListPath(repo), q, nil)
	if err != nil {
		return nil, err
	}
	var mrs []restMR
	if err := json.Unmarshal(resp.Body, &mrs); err != nil {
		return nil, fmt.Errorf("gitlab scm: decode open MR list: %w", err)
	}
	out := make([]ports.SCMPRObservation, 0, len(mrs))
	for _, mr := range mrs {
		out = append(out, mrToObservation(mr))
	}
	return out, nil
}

// RepoPRListGuard checks GitLab's cheap open-MR-list ETag guard.
func (p *Provider) RepoPRListGuard(ctx context.Context, repo ports.SCMRepo, etag string) (ports.SCMGuardResult, error) {
	q := url.Values{}
	q.Set("per_page", "1")
	resp, err := p.client.doRESTWithETag(ctx, mrListPath(repo), q, etag)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}
	return ports.SCMGuardResult{ETag: firstNonEmptyHeader(resp.ETag, etag), NotModified: resp.NotModified}, nil
}

// CommitChecksGuard checks GitLab's per-commit pipelines ETag guard.
func (p *Provider) CommitChecksGuard(ctx context.Context, repo ports.SCMRepo, headSHA, etag string) (ports.SCMGuardResult, error) {
	if strings.TrimSpace(headSHA) == "" {
		return ports.SCMGuardResult{}, fmt.Errorf("gitlab scm: empty head sha")
	}
	q := url.Values{}
	q.Set("sha", headSHA)
	q.Set("per_page", "1")
	resp, err := p.client.doRESTWithETag(ctx, pipelinesPath(repo), q, etag)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}
	return ports.SCMGuardResult{ETag: firstNonEmptyHeader(resp.ETag, etag), NotModified: resp.NotModified}, nil
}

// restMR is the subset of GitLab's merge request REST v4 payload this
// package normalizes. It is shared between ListOpenPRsByRepo (this file)
// and FetchPullRequests (observer detail fetch) so both map through the
// same mrToObservation helper.
type restMR struct {
	IID          int    `json:"iid"`
	State        string `json:"state"`
	Draft        bool   `json:"draft"`
	Title        string `json:"title"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	SHA          string `json:"sha"`
	WebURL       string `json:"web_url"`
	MergeStatus  string `json:"merge_status"`
	HasConflicts bool   `json:"has_conflicts"`
	// ChangesCount is a string in GitLab's API (e.g. "3", or "1000+" when the
	// diff is very large), not a number.
	ChangesCount string `json:"changes_count"`
	Author       struct {
		Username string `json:"username"`
	} `json:"author"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	MergedAt  string `json:"merged_at"`
	ClosedAt  string `json:"closed_at"`
}

// mrToObservation normalizes one GitLab merge request payload into the
// provider-neutral SCM DTO.
func mrToObservation(mr restMR) ports.SCMPRObservation {
	state, draft, merged, closed := normalizeMRState(mr.State, mr.Draft)
	changedFiles, _ := strconv.Atoi(mr.ChangesCount)
	return ports.SCMPRObservation{
		URL:               mr.WebURL,
		Number:            mr.IID,
		State:             state,
		Draft:             draft,
		Merged:            merged,
		Closed:            closed,
		SourceBranch:      mr.SourceBranch,
		TargetBranch:      mr.TargetBranch,
		HeadSHA:           mr.SHA,
		Title:             mr.Title,
		ChangedFiles:      changedFiles,
		Author:            mr.Author.Username,
		ProviderState:     mr.State,
		ProviderMergeable: mr.MergeStatus,
		HTMLURL:           mr.WebURL,
		CreatedAtProvider: parseGitLabTime(mr.CreatedAt),
		UpdatedAtProvider: parseGitLabTime(mr.UpdatedAt),
		MergedAtProvider:  parseGitLabTime(mr.MergedAt),
		ClosedAtProvider:  parseGitLabTime(mr.ClosedAt),
	}
}

// normalizeMRState maps GitLab's merge_request `state` enum plus the
// separate `draft` flag onto AO's normalized state string and booleans:
// "opened" -> open, "merged" -> merged, "locked"/"closed" -> closed. draft
// passes through unchanged since GitLab tracks it independently of state.
func normalizeMRState(state string, draft bool) (stateStr string, draftB, mergedB, closedB bool) {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "merged":
		stateStr = "merged"
		mergedB = true
	case "locked", "closed":
		stateStr = "closed"
		closedB = true
	default: // "opened" and any unrecognized state
		stateStr = "open"
	}
	draftB = draft
	return
}

// parseGitLabTime parses a GitLab REST timestamp (RFC3339), returning the
// zero time for blank/unparseable values instead of erroring, since these
// fields are optional (e.g. merged_at/closed_at are empty for open MRs).
func parseGitLabTime(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}
