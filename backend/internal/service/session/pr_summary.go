package session

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// PRSummary is the user-facing SCM read model for one PR owned by a session.
type PRSummary struct {
	URL              string
	HTMLURL          string
	Number           int
	Title            string
	State            domain.PRState
	Provider         string
	Repo             string
	Author           string
	SourceBranch     string
	TargetBranch     string
	HeadSHA          string
	Additions        int
	Deletions        int
	ChangedFiles     int
	CI               PRCISummary
	Review           PRReviewSummary
	Mergeability     PRMergeabilitySummary
	UpdatedAt        time.Time
	ObservedAt       time.Time
	CIObservedAt     time.Time
	ReviewObservedAt time.Time
}

// PRCISummary describes the latest CI status and failing checks for a PR.
type PRCISummary struct {
	State         domain.CIState
	FailingChecks []PRFailingCheck
}

// PRFailingCheck is one failed or cancelled CI check for a PR.
type PRFailingCheck struct {
	Name       string
	Status     domain.PRCheckStatus
	Conclusion string
	URL        string
}

// PRReviewSummary describes the latest review decision and unresolved comments,
// plus approval-progress facts (how many approved, of how many required, and
// which rule set the threshold) so the display surfaces can show A/T progress.
type PRReviewSummary struct {
	Decision                   domain.ReviewDecision
	HasUnresolvedHumanComments bool
	UnresolvedBy               []PRUnresolvedReviewer
	// ApprovalsCount is the number of distinct human approvers observed.
	ApprovalsCount int
	// RequiredApprovals is the effective approval threshold, or nil when no rule
	// applies or the SCM exposes no numeric threshold — nil ⇒ the surfaces keep
	// their pre-approval-progress behavior.
	RequiredApprovals *int
	// ApprovalRuleSource is which rule set the threshold: "scm" (the SCM's own
	// rule), "ao" (the project's additive rule), or "none". Empty for
	// merged/closed PRs, which carry no progress.
	ApprovalRuleSource string
}

// PRUnresolvedReviewer groups unresolved human comments by reviewer.
type PRUnresolvedReviewer struct {
	ReviewerID string
	Count      int
	Links      []PRReviewCommentLink
	ReviewURL  string
	IsBot      bool
}

// PRReviewCommentLink points to one unresolved review comment.
type PRReviewCommentLink struct {
	URL  string
	File string
	Line int
}

// PRMergeabilitySummary describes whether a PR can be merged and why.
type PRMergeabilitySummary struct {
	State         domain.Mergeability
	Reasons       []string
	PRURL         string
	ConflictFiles []PRConflictFile
}

// PRConflictFile is one file involved in a PR merge conflict.
type PRConflictFile struct {
	Path string
	URL  string
}

// ListPRSummaries returns all PRs owned by a session with concise SCM details
// assembled from persisted PR/check/review facts.
func (s *Service) ListPRSummaries(ctx context.Context, id domain.SessionID) ([]PRSummary, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", id, err)
	} else if !ok {
		return nil, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	// The project's approval rule sets the effective threshold for the
	// approval-progress facts. A missing/failed project lookup leaves the
	// zero-value (disabled) rule, so the summary degrades to no-progress.
	var approvalRule domain.ApprovalRule
	if project, ok, perr := s.store.GetProject(ctx, string(rec.ProjectID)); perr == nil && ok {
		approvalRule = project.Config.ApprovalRule
	}
	prs, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]PRSummary, 0, len(prs))
	for _, pr := range prs {
		checks, err := s.store.ListChecks(ctx, pr.URL)
		if err != nil {
			return nil, err
		}
		threads, err := s.store.ListPRReviewThreads(ctx, pr.URL)
		if err != nil {
			return nil, err
		}
		reviews, err := s.store.ListPRReviews(ctx, pr.URL)
		if err != nil {
			return nil, err
		}
		comments, err := s.store.ListPRComments(ctx, pr.URL)
		if err != nil {
			return nil, err
		}
		out = append(out, summarizePR(pr, checks, reviews, threads, comments, approvalRule))
	}
	sortPRSummaries(out)
	return out, nil
}

// providerForPRSummary returns the stored provider, or derives it from the URL
// shape when a (legacy/unpopulated) row has no provider: a GitLab merge-request
// URL carries "/-/merge_requests/", anything else defaults to GitHub. This keeps
// a GitLab MR from being mislabeled "github" in the display path.
func providerForPRSummary(pr domain.PullRequest) string {
	if p := strings.TrimSpace(pr.Provider); p != "" {
		return p
	}
	if strings.Contains(pr.URL, "/-/merge_requests/") {
		return "gitlab"
	}
	return "github"
}

func summarizePR(pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, rule domain.ApprovalRule) PRSummary {
	return PRSummary{
		URL:              pr.URL,
		HTMLURL:          firstNonEmpty(pr.HTMLURL, pr.URL),
		Number:           pr.Number,
		Title:            pr.Title,
		State:            pullRequestState(pr),
		Provider:         providerForPRSummary(pr),
		Repo:             pr.Repo,
		Author:           pr.Author,
		SourceBranch:     pr.SourceBranch,
		TargetBranch:     pr.TargetBranch,
		HeadSHA:          pr.HeadSHA,
		Additions:        pr.Additions,
		Deletions:        pr.Deletions,
		ChangedFiles:     pr.ChangedFiles,
		CI:               summarizeCI(pr, checks),
		Review:           summarizeReview(pr, comments, reviews, rule),
		Mergeability:     summarizeMergeability(pr, threads),
		UpdatedAt:        pr.UpdatedAt,
		ObservedAt:       pr.ObservedAt,
		CIObservedAt:     pr.CIObservedAt,
		ReviewObservedAt: pr.ReviewObservedAt,
	}
}

func summarizeCI(pr domain.PullRequest, checks []domain.PullRequestCheck) PRCISummary {
	state := ciOrUnknown(pr.CI)
	out := PRCISummary{State: state}
	if state != domain.CIFailing || pr.Merged || pr.Closed {
		return out
	}
	for _, ch := range checks {
		if ch.Status != domain.PRCheckFailed && ch.Status != domain.PRCheckCancelled {
			continue
		}
		if pr.HeadSHA != "" && ch.CommitHash != "" && !strings.EqualFold(ch.CommitHash, pr.HeadSHA) {
			continue
		}
		out.FailingChecks = append(out.FailingChecks, PRFailingCheck{
			Name:       ch.Name,
			Status:     ch.Status,
			Conclusion: ch.Conclusion,
			URL:        ch.URL,
		})
	}
	return out
}

// resolveApprovalProgress derives the approval-progress facts for a live PR:
// the approved count, the effective required threshold (nil when unknown), and
// which rule set it. The SCM's own rule wins (guardrail: AO never imposes its
// threshold when the SCM enforces one); otherwise the project's additive rule
// applies; otherwise no threshold is known.
func resolveApprovalProgress(pr domain.PullRequest, rule domain.ApprovalRule) (count int, required *int, source string) {
	count = pr.ApprovalsCount
	switch {
	case pr.ApprovalRuleConfigured:
		// SCM enforces its own rule. Surface its required count when it exposes a
		// number (GitLab); otherwise degrade to count-only (e.g. GitHub).
		if pr.ApprovalsRequired > 0 {
			n := pr.ApprovalsRequired
			required = &n
		}
		return count, required, "scm"
	case rule.Enabled && providerReportsApprovals(pr):
		// AO's additive rule sets the threshold, but only where the provider
		// actually reports approval counts. On a provider that does not (GitHub),
		// the count is always 0 and a meter would show a misleading 0/T that
		// contradicts the provider's own approved decision — so degrade to none.
		n := rule.ResolveThreshold()
		return count, &n, "ao"
	default:
		return count, nil, "none"
	}
}

// providerReportsApprovals reports whether the PR's provider populates approval
// counts. Only GitLab does today; other adapters leave ApprovalsCount at zero,
// so an AO count-based rule cannot be surfaced as progress for them.
func providerReportsApprovals(pr domain.PullRequest) bool {
	return providerForPRSummary(pr) == "gitlab"
}

func summarizeReview(pr domain.PullRequest, comments []domain.PullRequestComment, reviews []domain.PullRequestReview, rule domain.ApprovalRule) PRReviewSummary {
	out := PRReviewSummary{Decision: reviewOrNone(pr.Review)}
	if pr.Merged || pr.Closed {
		return out
	}
	out.ApprovalsCount, out.RequiredApprovals, out.ApprovalRuleSource = resolveApprovalProgress(pr, rule)
	byReviewer := map[string]int{}
	order := []string{}
	links := map[string][]PRReviewCommentLink{}
	isBot := map[string]bool{}
	for _, c := range comments {
		if c.Resolved || c.IsBot {
			continue
		}
		reviewer := strings.TrimSpace(c.Author)
		if reviewer == "" {
			reviewer = "unknown"
		}
		if _, ok := byReviewer[reviewer]; !ok {
			order = append(order, reviewer)
		}
		byReviewer[reviewer]++
		isBot[reviewer] = c.IsBot
		links[reviewer] = append(links[reviewer], PRReviewCommentLink{
			URL:  c.URL,
			File: c.File,
			Line: c.Line,
		})
	}
	reviewURLByAuthor := map[string]string{}
	for reviewer, review := range latestChangesRequestedReviews(reviews) {
		if _, ok := byReviewer[reviewer]; !ok {
			order = append(order, reviewer)
		}
		reviewURLByAuthor[reviewer] = review.URL
		isBot[reviewer] = review.IsBot
	}
	sort.Strings(order)
	for _, reviewer := range order {
		out.UnresolvedBy = append(out.UnresolvedBy, PRUnresolvedReviewer{
			ReviewerID: reviewer,
			Count:      byReviewer[reviewer],
			Links:      links[reviewer],
			ReviewURL:  reviewURLByAuthor[reviewer],
			IsBot:      isBot[reviewer],
		})
	}
	for _, reviewer := range out.UnresolvedBy {
		if reviewer.Count > 0 && !reviewer.IsBot {
			out.HasUnresolvedHumanComments = true
			break
		}
	}
	return out
}

func latestChangesRequestedReviews(reviews []domain.PullRequestReview) map[string]domain.PullRequestReview {
	latestByReviewer := map[string]domain.PullRequestReview{}
	for _, review := range reviews {
		if review.State != domain.ReviewChangesRequest && review.State != domain.ReviewApproved {
			continue
		}
		reviewer := strings.TrimSpace(review.Author)
		if reviewer == "" {
			reviewer = "unknown"
		}
		current, ok := latestByReviewer[reviewer]
		if !ok || reviewAfter(review, current) {
			latestByReviewer[reviewer] = review
		}
	}
	out := map[string]domain.PullRequestReview{}
	for reviewer, review := range latestByReviewer {
		if review.State == domain.ReviewChangesRequest {
			out[reviewer] = review
		}
	}
	return out
}

func reviewAfter(a, b domain.PullRequestReview) bool {
	if a.SubmittedAt.IsZero() || b.SubmittedAt.IsZero() {
		return a.SubmittedAt.IsZero() == b.SubmittedAt.IsZero() && a.ID > b.ID
	}
	if a.SubmittedAt.Equal(b.SubmittedAt) {
		return a.ID > b.ID
	}
	return a.SubmittedAt.After(b.SubmittedAt)
}

func summarizeMergeability(pr domain.PullRequest, _ []domain.PullRequestReviewThread) PRMergeabilitySummary {
	return PRMergeabilitySummary{
		State:   mergeabilityOrUnknown(pr.Mergeability),
		Reasons: mergeabilityReasons(pr),
		PRURL:   firstNonEmpty(pr.HTMLURL, pr.URL),
	}
}

func mergeabilityReasons(pr domain.PullRequest) []string {
	if pr.Merged || pr.Closed {
		return nil
	}
	if pr.Mergeability != domain.MergeConflicting && pr.Mergeability != domain.MergeBlocked && pr.Mergeability != domain.MergeUnstable {
		return nil
	}
	reasons := map[string]bool{}
	add := func(reason string) {
		if reason != "" {
			reasons[reason] = true
		}
	}
	if pr.Mergeability == domain.MergeConflicting || containsAny(pr.ProviderMergeable, "conflict", "dirty") || containsAny(pr.ProviderMergeStateStatus, "conflict", "dirty") {
		add("conflicts")
	}
	if containsAny(pr.ProviderMergeStateStatus, "behind") {
		add("behind_base")
	}
	if pr.Draft {
		add("draft")
	}
	if pr.CI == domain.CIFailing {
		add("ci_failing")
	}
	if pr.Review == domain.ReviewChangesRequest {
		add("changes_requested")
	}
	if pr.Review == domain.ReviewRequired {
		add("review_required")
	}
	if pr.Mergeability == domain.MergeBlocked && len(reasons) == 0 {
		add("blocked_by_provider")
	}
	if pr.Mergeability == domain.MergeUnstable && len(reasons) == 0 {
		add("blocked_by_provider")
	}
	out := make([]string, 0, len(reasons))
	for reason := range reasons {
		out = append(out, reason)
	}
	sort.Strings(out)
	return out
}

func containsAny(s string, needles ...string) bool {
	s = strings.ToLower(s)
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func sortPRSummaries(prs []PRSummary) {
	sort.SliceStable(prs, func(i, j int) bool {
		ia, ja := prSummaryActive(prs[i]), prSummaryActive(prs[j])
		if ia != ja {
			return ia
		}
		return prs[i].UpdatedAt.After(prs[j].UpdatedAt)
	})
}

func prSummaryActive(pr PRSummary) bool {
	return pr.State != domain.PRStateMerged && pr.State != domain.PRStateClosed
}

func pullRequestState(pr domain.PullRequest) domain.PRState {
	switch {
	case pr.Merged:
		return domain.PRStateMerged
	case pr.Closed:
		return domain.PRStateClosed
	case pr.Draft:
		return domain.PRStateDraft
	default:
		return domain.PRStateOpen
	}
}

func ciOrUnknown(state domain.CIState) domain.CIState {
	if state == "" {
		return domain.CIUnknown
	}
	return state
}

func reviewOrNone(decision domain.ReviewDecision) domain.ReviewDecision {
	if decision == "" {
		return domain.ReviewNone
	}
	return decision
}

func mergeabilityOrUnknown(state domain.Mergeability) domain.Mergeability {
	if state == "" {
		return domain.MergeUnknown
	}
	return state
}
