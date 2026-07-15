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
		out = append(out, mrToObservation(mr, repo))
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

// BaseBranchGuard checks GitLab's per-branch ETag guard so the observer can tell
// when an MR's target branch head advanced (a sibling MR merged into the shared
// base) without changing the MR's own source head SHA.
func (p *Provider) BaseBranchGuard(ctx context.Context, repo ports.SCMRepo, branch, etag string) (ports.SCMGuardResult, error) {
	if strings.TrimSpace(branch) == "" {
		return ports.SCMGuardResult{}, fmt.Errorf("gitlab scm: empty base branch")
	}
	path := "projects/" + projectID(repo) + "/repository/branches/" + url.PathEscape(branch)
	resp, err := p.client.doRESTWithETag(ctx, path, nil, etag)
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
	IID   int    `json:"iid"`
	State string `json:"state"`
	Draft bool   `json:"draft"`
	Title string `json:"title"`
	// SourceProjectID/TargetProjectID identify the projects the MR's source and
	// target branches live in. They are equal for a same-project MR (the AO
	// worker model, where a session pushes its branch to the origin project) and
	// differ for a fork MR. mrToObservation uses their relationship to fill
	// HeadRepo, which branch-prefix attribution requires.
	SourceProjectID int    `json:"source_project_id"`
	TargetProjectID int    `json:"target_project_id"`
	SourceBranch    string `json:"source_branch"`
	TargetBranch    string `json:"target_branch"`
	SHA             string `json:"sha"`
	// DiffRefs carries the diff base/head/start SHAs. GitLab populates it on the
	// single-MR detail endpoint (not the list endpoint); base_sha is the diff
	// base needed to render review-comment code context (`git diff base..head`).
	DiffRefs struct {
		BaseSHA  string `json:"base_sha"`
		HeadSHA  string `json:"head_sha"`
		StartSHA string `json:"start_sha"`
	} `json:"diff_refs"`
	WebURL      string `json:"web_url"`
	MergeStatus string `json:"merge_status"`
	// DetailedMergeStatus is GitLab's richer, authoritative merge verdict
	// (GitLab >= 15.6). Unlike the coarse MergeStatus — which returns
	// can_be_merged whenever conflicts/pipeline are clear, even with an unmet
	// approval rule — it names the specific blocker (e.g. not_approved,
	// discussions_not_resolved, ci_must_pass). Empty on older GitLab.
	DetailedMergeStatus string `json:"detailed_merge_status"`
	HasConflicts        bool   `json:"has_conflicts"`
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
// provider-neutral SCM DTO. repo is the project the MR was listed/fetched from
// (its target project); it supplies HeadRepo for same-project MRs.
func mrToObservation(mr restMR, repo ports.SCMRepo) ports.SCMPRObservation {
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
		HeadRepo:          headRepoFullName(mr, repo),
		TargetBranch:      mr.TargetBranch,
		HeadSHA:           mr.SHA,
		BaseSHA:           mr.DiffRefs.BaseSHA,
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

// headRepoFullName returns the full path (group/.../project) of the project the
// MR's source branch lives in. The SCM observer's branch-prefix attribution
// only claims an open PR whose HeadRepo equals a session's push origin, and
// drops any PR with an empty HeadRepo (candidatesForHeadRepo in
// internal/observe/scm/observer.go) — so leaving this blank, as the adapter
// previously did, silently strands every GitLab MR in the WORKING column.
//
// For a same-project MR (SourceProjectID == TargetProjectID) the source branch
// lives in the listed/target project, so its full path is repo.Repo. This is
// the AO worker model: a session pushes its branch to the origin project. GitLab
// always reports both ids, so a fork MR (SourceProjectID != TargetProjectID)
// takes the other path; its source project path is not carried in the MR list
// payload and AO workers never push from a fork, so HeadRepo is left empty and
// the observer leaves the MR unattributed rather than misattributing it to an
// origin session — mirroring the no-misattribution guard the GitHub path relies
// on. (Zero-valued ids in a minimal payload compare equal and fall through to
// the same-project branch, the correct default for AO's usage.)
func headRepoFullName(mr restMR, repo ports.SCMRepo) string {
	if mr.SourceProjectID == mr.TargetProjectID {
		return repo.Repo
	}
	return ""
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
		jobs, err = p.fetchAllPipelineJobs(ctx, ref.Repo, pipeline.ID)
		if err != nil {
			return ports.SCMObservation{}, err
		}
	}

	return ports.SCMObservation{
		Fetched:      true,
		ObservedAt:   time.Now(),
		Provider:     "gitlab",
		Host:         ref.Repo.Host,
		Repo:         ref.Repo.Repo,
		PR:           mrToObservation(mr, ref.Repo),
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
		status := normalizeJobStatus(job.Status)
		check := ports.SCMCheckObservation{
			Name: job.Name,
			// Status must be one of AO's normalized domain.PRCheckStatus values;
			// the raw GitLab status is kept in Conclusion for detail.
			Status:     string(status),
			Conclusion: job.Status,
			URL:        job.WebURL,
			ProviderID: strconv.Itoa(job.ID),
		}
		checks = append(checks, check)
		if status == domain.PRCheckFailed || status == domain.PRCheckCancelled {
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

// normalizeJobStatus maps GitLab's CI job `status` enum onto AO's normalized
// per-check status (domain.PRCheckStatus). The pr_checks.status column
// CHECK-constrains writes to that vocabulary, so emitting GitLab's raw statuses
// (success/canceled/running/manual/…) makes the whole PR observation write fail
// atomically — dropping title, CI, mergeability, and observed_at for the MR.
// Mirrors the github adapter, whose SCMCheckObservation.Status is likewise a
// normalized domain value. This is the per-job status; normalizeCIStatus below
// maps the pipeline-level summary onto the separate CI-summary vocabulary.
func normalizeJobStatus(status string) domain.PRCheckStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success":
		return domain.PRCheckPassed
	case "failed":
		return domain.PRCheckFailed
	case "canceled", "cancelled":
		return domain.PRCheckCancelled
	case "skipped":
		return domain.PRCheckSkipped
	case "running", "preparing":
		return domain.PRCheckInProgress
	case "created", "pending", "scheduled", "waiting_for_resource", "manual":
		return domain.PRCheckQueued
	default:
		return domain.PRCheckUnknown
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

// mergeability normalizes a merge request's merge status into AO's mergeability
// verdict. GitLab reports has_conflicts independently of merge_status, so
// conflicts are surfaced as an explicit blocker even when merge_status doesn't
// otherwise call it out. The Mergeable flag and conflict blocker are derived
// from the normalized State so that whichever signal decided it (the rich
// detailed_merge_status, the coarse merge_status, or has_conflicts) stays
// consistent with the emitted verdict.
func mergeability(mr restMR) ports.SCMMergeabilityObservation {
	state := normalizeMergeStatus(mr.MergeStatus, mr.DetailedMergeStatus, mr.HasConflicts)
	out := ports.SCMMergeabilityObservation{
		State:     state,
		Mergeable: state == string(domain.MergeMergeable),
	}
	if state == string(domain.MergeConflicting) {
		out.Conflict = true
		out.Blockers = append(out.Blockers, "merge conflict")
	}
	return out
}

// normalizeMergeStatus maps GitLab's merge-status vocabulary onto AO's
// domain.Mergeability enum. This mirrors the github adapter, which folds the
// review/CI/blocked gates into its verdict: the observer casts State straight
// into domain.Mergeability, and the status pipeline only reaches "Ready to
// merge" when it equals domain.MergeMergeable.
//
// detailed_merge_status (GitLab >= 15.6) is preferred because it is the only
// signal that reflects approval rules. The coarse merge_status returns
// can_be_merged even when an approval rule (e.g. requires >= 3 approvals) is
// unmet, which is exactly how an under-approved MR was wrongly promoted to
// "Ready to merge". Only detailed_merge_status == "mergeable" means the MR can
// actually merge right now; every other terminal value is a blocker, and the
// transient checking/unchecked states are unknown. When detailed_merge_status
// is absent (older GitLab / minimal payloads) fall back to the legacy
// merge_status mapping. has_conflicts always wins, since GitLab reports it
// independently.
func normalizeMergeStatus(mergeStatus, detailedMergeStatus string, hasConflicts bool) string {
	if hasConflicts {
		return string(domain.MergeConflicting)
	}
	if d := strings.ToLower(strings.TrimSpace(detailedMergeStatus)); d != "" {
		switch d {
		case "mergeable":
			return string(domain.MergeMergeable)
		case "conflict", "broken_status":
			return string(domain.MergeConflicting)
		case "checking", "unchecked", "preparing":
			return string(domain.MergeUnknown)
		default:
			// not_approved, blocked_status, draft_status, discussions_not_resolved,
			// requested_changes, ci_must_pass, ci_still_running, need_rebase,
			// external_status_checks, not_open, jira_association_missing, ... —
			// all mean "cannot merge right now".
			return string(domain.MergeBlocked)
		}
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
	System     bool   `json:"system"`
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
	ApprovalsLeft     int  `json:"approvals_left"`
	ApprovalsRequired int  `json:"approvals_required"`
	HasApprovalRules  bool `json:"has_approval_rules"`
	ApprovedBy        []struct {
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

	discussions, err := p.fetchAllDiscussions(ctx, mrPath)
	if err != nil {
		return ports.SCMReviewObservation{}, err
	}

	resp, err := p.client.doREST(ctx, mrPath+"/approvals", nil)
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
		Decision:               approvalDecision(approvals),
		ApprovalsCount:         len(approvals.ApprovedBy),
		ApprovalsRequired:      approvals.ApprovalsRequired,
		ApprovalRuleConfigured: approvalRuleConfigured(approvals),
		Threads:                threads,
		Partial:                false,
	}, nil
}

// maxDiscussionPages bounds the discussions pagination loop as a safety net
// against a server that never signals a final page. 100 pages × 100 per page =
// 10k discussions, far beyond any real MR.
const maxDiscussionPages = 100

// maxPipelineJobPages bounds the pipeline-jobs pagination loop. 20 pages × 100
// per page = 2k jobs, far beyond any real pipeline.
const maxPipelineJobPages = 20

// fetchAllPipelineJobs pages through a pipeline's jobs endpoint and returns every
// job. GitLab paginates /pipelines/:id/jobs (default 20, max 100 per page) and
// orders jobs newest-id-first, so a large pipeline's EARLY-stage jobs (e.g. a
// lint gate) land on later pages. Reading only the first page silently dropped
// them: a pipeline could report status=failed while every job AO saw was
// skipped, leaving CI.FailedChecks empty — so the CI-fail nudge (which needs a
// failed check row) never fired even though ci_state was failing. Same class of
// bug as the discussions page-1-only truncation (fix/reviews-gitlab-resolved-
// refresh). Offset pagination guarantees a short page (< perPage) is the last.
func (p *Provider) fetchAllPipelineJobs(ctx context.Context, repo ports.SCMRepo, pipelineID int) ([]restJob, error) {
	const perPage = 100
	jobsPath := "projects/" + projectID(repo) + "/pipelines/" + strconv.Itoa(pipelineID) + "/jobs"
	var all []restJob
	for page := 1; page <= maxPipelineJobPages; page++ {
		q := url.Values{}
		q.Set("per_page", strconv.Itoa(perPage))
		q.Set("page", strconv.Itoa(page))
		resp, err := p.client.doREST(ctx, jobsPath, q)
		if err != nil {
			return nil, err
		}
		var batch []restJob
		if err := json.Unmarshal(resp.Body, &batch); err != nil {
			return nil, fmt.Errorf("gitlab scm: decode pipeline jobs page %d: %w", page, err)
		}
		all = append(all, batch...)
		if len(batch) < perPage {
			break
		}
	}
	return all, nil
}

// fetchAllDiscussions pages through GitLab's MR discussions endpoint and returns
// every discussion. GitLab paginates discussions (default 20, max 100 per page)
// and system notes count toward the total, so an active MR easily exceeds one
// page. Reading only the first page silently dropped the newest review threads
// (and any later resolve/unresolve of them), freezing AO's Resolved/unresolved
// view once the MR got busy — see fix/reviews-gitlab-resolved-refresh. Offset
// pagination guarantees a short page (< perPage, including empty) is the last
// one, so the loop stops there.
func (p *Provider) fetchAllDiscussions(ctx context.Context, mrPath string) ([]restDiscussion, error) {
	const perPage = 100
	var all []restDiscussion
	for page := 1; page <= maxDiscussionPages; page++ {
		q := url.Values{}
		q.Set("per_page", strconv.Itoa(perPage))
		q.Set("page", strconv.Itoa(page))
		resp, err := p.client.doREST(ctx, mrPath+"/discussions", q)
		if err != nil {
			return nil, err
		}
		var batch []restDiscussion
		if err := json.Unmarshal(resp.Body, &batch); err != nil {
			return nil, fmt.Errorf("gitlab scm: decode MR discussions page %d: %w", page, err)
		}
		all = append(all, batch...)
		if len(batch) < perPage {
			break
		}
	}
	return all, nil
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
			System: n.System,
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

// approvalDecision maps GitLab's approvals payload onto AO's normalized review
// decision. When GitLab enforces an approval rule, "approved" means no approvals
// remain outstanding and at least one is recorded. When there is NO rule
// (approvals_left is trivially 0), GitLab cannot express a floor, so we return no
// decision and let the project's ApprovalRule (if enabled) decide from
// ApprovalsCount at status-derivation time.
func approvalDecision(a restApprovals) string {
	if !approvalRuleConfigured(a) {
		return ""
	}
	if a.ApprovalsLeft == 0 && len(a.ApprovedBy) > 0 {
		return "approved"
	}
	return ""
}

// approvalRuleConfigured reports whether GitLab enforces an approval rule of its
// own for this MR.
func approvalRuleConfigured(a restApprovals) bool {
	return a.ApprovalsRequired > 0 || a.HasApprovalRules
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
