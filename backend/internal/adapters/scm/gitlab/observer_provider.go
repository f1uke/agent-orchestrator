package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ciFailureLogTailLines is the number of trailing lines of a failed job's
// trace to surface, mirroring the github adapter's const of the same name
// (backend/internal/adapters/scm/github/provider.go).
const ciFailureLogTailLines = 20

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
	resp, err := p.client.doREST(ctx, mrListPath(repo), q)
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
// merged -> "merged", locked/closed -> "closed", else draft -> "draft",
// else "opened" -> "open". This mirrors the GitHub adapter's
// normalizePRState (backend/internal/adapters/scm/github/observer_provider.go)
// and the ports.SCMPRObservation.State doc ("draft, open, merged, or
// closed"), since consumers (backend/internal/storage/sqlite/store/pr_facts.go)
// derive draft solely from State == domain.PRStateDraft — keeping draft
// only as a side boolean would lose the draft signal in the display path.
// The Draft/Merged/Closed booleans on the observation are unaffected: Draft
// stays true for a draft MR regardless of how State is folded.
func normalizeMRState(state string, draft bool) (stateStr string, draftB, mergedB, closedB bool) {
	draftB = draft
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "merged":
		stateStr = "merged"
		mergedB = true
	case "locked", "closed":
		stateStr = "closed"
		closedB = true
	default: // "opened" and any unrecognized state
		if draft {
			stateStr = "draft"
		} else {
			stateStr = "open"
		}
	}
	return
}

// restPipeline is the subset of GitLab's pipeline REST v4 payload used to
// pick the pipeline matching a merge request's head SHA and to derive the
// normalized CI summary.
type restPipeline struct {
	ID     int    `json:"id"`
	SHA    string `json:"sha"`
	Status string `json:"status"`
}

// restJob is the subset of GitLab's pipeline job REST v4 payload normalized
// into ports.SCMCheckObservation.
type restJob struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	WebURL string `json:"web_url"`
}

// FetchPullRequests fetches normalized PR/CI/mergeability metadata for each
// ref via GitLab's REST v4 API: MR detail, then the MR's pipelines (matched
// to the MR's head SHA), then that pipeline's jobs. Refs are fetched
// independently so one ref's fetch failure does not fail the whole batch;
// a failing ref yields an unfetched observation (Fetched: false) rather than
// being dropped or inferred as closed, per the observer's contract.
func (p *Provider) FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	out := make([]ports.SCMObservation, 0, len(refs))
	for _, ref := range refs {
		obs, err := p.fetchOnePullRequest(ctx, ref)
		if err != nil {
			out = append(out, ports.SCMObservation{Fetched: false})
			continue
		}
		out = append(out, obs)
	}
	return out, nil
}

func (p *Provider) fetchOnePullRequest(ctx context.Context, ref ports.SCMPRRef) (ports.SCMObservation, error) {
	mrPath := "projects/" + projectID(ref.Repo) + "/merge_requests/" + strconv.Itoa(ref.Number)
	resp, err := p.client.doREST(ctx, mrPath, nil)
	if err != nil {
		return ports.SCMObservation{}, err
	}
	var mr restMR
	if err := json.Unmarshal(resp.Body, &mr); err != nil {
		return ports.SCMObservation{}, fmt.Errorf("gitlab scm: decode MR detail: %w", err)
	}

	resp, err = p.client.doREST(ctx, mrPath+"/pipelines", nil)
	if err != nil {
		return ports.SCMObservation{}, err
	}
	var pipelines []restPipeline
	if err := json.Unmarshal(resp.Body, &pipelines); err != nil {
		return ports.SCMObservation{}, fmt.Errorf("gitlab scm: decode MR pipelines: %w", err)
	}
	pipeline := latestPipelineForSHA(pipelines, mr.SHA)

	var jobs []restJob
	if pipeline.ID != 0 {
		jobsPath := "projects/" + projectID(ref.Repo) + "/pipelines/" + strconv.Itoa(pipeline.ID) + "/jobs"
		resp, err = p.client.doREST(ctx, jobsPath, nil)
		if err != nil {
			return ports.SCMObservation{}, err
		}
		if err := json.Unmarshal(resp.Body, &jobs); err != nil {
			return ports.SCMObservation{}, fmt.Errorf("gitlab scm: decode pipeline jobs: %w", err)
		}
	}

	return ports.SCMObservation{
		Fetched:      true,
		ObservedAt:   time.Now(),
		Provider:     "gitlab",
		Host:         ref.Repo.Host,
		Repo:         ref.Repo.Repo,
		PR:           mrToObservation(mr),
		CI:           ciObservation(pipeline, jobs),
		Mergeability: mergeability(mr),
	}, nil
}

// FetchFailedCheckLogTail fetches and tails a failed GitLab job's trace.
// GitLab serves job traces as plain text (not JSON), so the raw response
// body is used directly rather than JSON-decoded.
func (p *Provider) FetchFailedCheckLogTail(ctx context.Context, repo ports.SCMRepo, check ports.SCMCheckObservation) (string, error) {
	if check.ProviderID == "" {
		return "", nil
	}
	tracePath := "projects/" + projectID(repo) + "/jobs/" + check.ProviderID + "/trace"
	resp, err := p.client.doREST(ctx, tracePath, nil)
	if err != nil {
		return "", err
	}
	return lastNLines(string(resp.Body), ciFailureLogTailLines), nil
}

// lastNLines returns the last n newline-separated lines of s, normalizing
// CRLF to LF and trimming surrounding whitespace first. If s has n or fewer
// lines, it is returned unchanged (minus the trim/normalize).
func lastNLines(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\r\n", "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// latestPipelineForSHA returns the newest pipeline (by id) whose sha matches
// headSHA, or the zero restPipeline if none match. GitLab's pipelines
// endpoint is not guaranteed to be sorted, so this always compares ids
// rather than assuming the first match is newest.
func latestPipelineForSHA(pipelines []restPipeline, headSHA string) restPipeline {
	var latest restPipeline
	for _, pl := range pipelines {
		if pl.SHA != headSHA {
			continue
		}
		if pl.ID > latest.ID {
			latest = pl
		}
	}
	return latest
}

// ciObservation normalizes one pipeline plus its jobs into the
// provider-neutral CI DTO. jobs may be empty when the matched pipeline has
// no jobs yet or no pipeline matched the MR's head SHA.
func ciObservation(pipeline restPipeline, jobs []restJob) ports.SCMCIObservation {
	checks := make([]ports.SCMCheckObservation, 0, len(jobs))
	failed := make([]ports.SCMCheckObservation, 0)
	for _, job := range jobs {
		check := ports.SCMCheckObservation{
			Name:       job.Name,
			Status:     job.Status,
			Conclusion: job.Status,
			URL:        job.WebURL,
			ProviderID: strconv.Itoa(job.ID),
		}
		checks = append(checks, check)
		if job.Status == "failed" || job.Status == "canceled" {
			failed = append(failed, check)
		}
	}
	return ports.SCMCIObservation{
		Summary:      normalizeCIStatus(pipeline.Status),
		HeadSHA:      pipeline.SHA,
		Checks:       checks,
		FailedChecks: failed,
	}
}

// normalizeCIStatus maps GitLab's pipeline `status` enum onto AO's
// normalized CI summary: unknown, pending, passing, or failing.
func normalizeCIStatus(pipelineStatus string) string {
	switch strings.ToLower(strings.TrimSpace(pipelineStatus)) {
	case "success":
		return "passing"
	case "failed":
		return "failing"
	case "running", "pending", "created", "scheduled":
		return "pending"
	default:
		return "unknown"
	}
}

// mergeability normalizes a merge request's merge_status/has_conflicts into
// AO's mergeability verdict. GitLab reports has_conflicts independently of
// merge_status, so conflicts are surfaced as an explicit blocker even when
// merge_status doesn't otherwise call it out.
func mergeability(mr restMR) ports.SCMMergeabilityObservation {
	out := ports.SCMMergeabilityObservation{
		State:     normalizeMergeStatus(mr.MergeStatus, mr.HasConflicts),
		Mergeable: mr.MergeStatus == "can_be_merged" && !mr.HasConflicts,
	}
	if mr.HasConflicts {
		out.Conflict = true
		out.Blockers = append(out.Blockers, "merge conflict")
	}
	return out
}

// normalizeMergeStatus maps GitLab's raw merge_status vocabulary (plus the
// independently-reported has_conflicts flag) onto AO's domain.Mergeability
// enum. This mirrors the github adapter, which also emits domain enum values:
// the observer casts State straight into domain.Mergeability, and the status
// pipeline only reaches "Ready to merge" when it equals domain.MergeMergeable.
// Emitting GitLab's raw "can_be_merged" would never match, stranding mergeable
// MRs in the "In review" column.
func normalizeMergeStatus(mergeStatus string, hasConflicts bool) string {
	if hasConflicts {
		return string(domain.MergeConflicting)
	}
	switch strings.ToLower(strings.TrimSpace(mergeStatus)) {
	case "can_be_merged":
		return string(domain.MergeMergeable)
	case "cannot_be_merged":
		return string(domain.MergeBlocked)
	default: // "unchecked", "checking", "", or any future/unknown value
		return string(domain.MergeUnknown)
	}
}

// restDiscussion is the subset of GitLab's MR discussions REST v4 payload
// normalized into ports.SCMReviewThreadObservation. A "discussion" is
// GitLab's grouping of one or more notes that share a single thread (e.g. a
// diff comment and its replies).
type restDiscussion struct {
	ID    string     `json:"id"`
	Notes []restNote `json:"notes"`
}

// restNote is one note within a discussion. Resolvable is true only for
// diff-anchored review comments (plain MR comments are never resolvable),
// so it doubles as the signal for whether a discussion is a review thread
// at all versus ordinary conversation.
type restNote struct {
	ID         int    `json:"id"`
	Body       string `json:"body"`
	Resolvable bool   `json:"resolvable"`
	Resolved   bool   `json:"resolved"`
	Author     struct {
		Username string `json:"username"`
	} `json:"author"`
	Position *struct {
		NewPath string `json:"new_path"`
		NewLine int    `json:"new_line"`
	} `json:"position"`
}

// restApprovals is the subset of GitLab's MR approvals REST v4 payload used
// to derive the normalized review decision.
type restApprovals struct {
	ApprovalsLeft int `json:"approvals_left"`
	ApprovedBy    []struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	} `json:"approved_by"`
}

// FetchReviewThreads fetches review discussions and the approval decision
// for one merge request via GitLab's REST v4 API. GitLab has no cheap
// partial/incremental discussions endpoint, so unlike GitHub's cursor-paged
// review threads, this always returns a full per-poll snapshot (Partial:
// false) for round 1.
func (p *Provider) FetchReviewThreads(ctx context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	mrPath := "projects/" + projectID(ref.Repo) + "/merge_requests/" + strconv.Itoa(ref.Number)

	resp, err := p.client.doREST(ctx, mrPath+"/discussions", nil)
	if err != nil {
		return ports.SCMReviewObservation{}, err
	}
	var discussions []restDiscussion
	if err := json.Unmarshal(resp.Body, &discussions); err != nil {
		return ports.SCMReviewObservation{}, fmt.Errorf("gitlab scm: decode MR discussions: %w", err)
	}

	resp, err = p.client.doREST(ctx, mrPath+"/approvals", nil)
	if err != nil {
		return ports.SCMReviewObservation{}, err
	}
	var approvals restApprovals
	if err := json.Unmarshal(resp.Body, &approvals); err != nil {
		return ports.SCMReviewObservation{}, fmt.Errorf("gitlab scm: decode MR approvals: %w", err)
	}

	threads := make([]ports.SCMReviewThreadObservation, 0, len(discussions))
	for _, d := range discussions {
		thread, ok := discussionToThread(d)
		if !ok {
			continue
		}
		threads = append(threads, thread)
	}

	return ports.SCMReviewObservation{
		Decision: approvalDecision(approvals),
		Threads:  threads,
		Partial:  false,
	}, nil
}

// discussionToThread normalizes one discussion into a review thread. It
// returns ok=false for discussions with no notes or with no resolvable note,
// since only diff-anchored, resolvable notes represent a review thread as
// opposed to an ordinary MR comment.
func discussionToThread(d restDiscussion) (thread ports.SCMReviewThreadObservation, ok bool) {
	if len(d.Notes) == 0 {
		return ports.SCMReviewThreadObservation{}, false
	}

	hasResolvable := false
	resolved := true
	allBot := true
	comments := make([]ports.SCMReviewCommentObservation, 0, len(d.Notes))
	for _, n := range d.Notes {
		if n.Resolvable {
			hasResolvable = true
			if !n.Resolved {
				resolved = false
			}
		}
		isBot := isBotUsername(n.Author.Username)
		if !isBot {
			allBot = false
		}
		comments = append(comments, ports.SCMReviewCommentObservation{
			ID:     strconv.Itoa(n.ID),
			Author: n.Author.Username,
			Body:   n.Body,
			IsBot:  isBot,
		})
	}
	if !hasResolvable {
		return ports.SCMReviewThreadObservation{}, false
	}

	var path string
	var line int
	if first := d.Notes[0]; first.Position != nil {
		path = first.Position.NewPath
		line = first.Position.NewLine
	}

	return ports.SCMReviewThreadObservation{
		ID:       d.ID,
		Path:     path,
		Line:     line,
		Resolved: resolved,
		IsBot:    allBot,
		Comments: comments,
	}, true
}

// approvalDecision maps GitLab's approvals payload onto AO's normalized
// review decision: "approved" once no approvals remain outstanding and at
// least one approval has been recorded, else empty (no decision yet).
func approvalDecision(a restApprovals) string {
	if a.ApprovalsLeft == 0 && len(a.ApprovedBy) > 0 {
		return "approved"
	}
	return ""
}

// isBotUsername is a best-effort bot signal for GitLab authors. GitLab's
// note/approval author payload (UserBasic) carries no typed bot flag, so we
// match GitLab's bot-account username convention rather than a raw "bot"
// substring. The bare strings.Contains(login, "bot") approach was dropped
// from the GitHub adapter (see scm/github/provider.go) because logins like
// "robothon"/"lambot123" tripped it; underscore-delimited matching avoids
// that while still catching project_<id>_bot_<hex> / group_<id>_bot_<hex>
// and *_bot service accounts.
func isBotUsername(username string) bool {
	u := strings.ToLower(strings.TrimSpace(username))
	return strings.HasSuffix(u, "_bot") || strings.Contains(u, "_bot_")
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
