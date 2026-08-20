package review

import (
	"sort"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// StateStatus is the per-PR review planning state.
type StateStatus string

const (
	// ReviewStateNeedsReview means an available PR has no current AO approval or running pass.
	ReviewStateNeedsReview StateStatus = "needs_review"
	// ReviewStateRunning means a review run is already active for the PR's current head.
	ReviewStateRunning StateStatus = "running"
	// ReviewStateUpToDate means AO approved the PR's current head.
	ReviewStateUpToDate StateStatus = "up_to_date"
	// ReviewStateChangesRequested means AO requested changes on the PR's current head.
	ReviewStateChangesRequested StateStatus = "changes_requested"
	// ReviewStateIneligible means the PR is closed, merged, or missing required facts.
	// Draft is NOT ineligible: see the eligibility check in Plan.
	ReviewStateIneligible StateStatus = "ineligible"
)

// PRReviewState is one review decision for a worker session. It is normally
// PR-scoped; a pre-MR state (PlanBranch) carries an empty PRURL, a zero PRNumber
// and the checkout's Branch instead.
type PRReviewState struct {
	PRURL    string `json:"prUrl"`
	PRNumber int    `json:"prNumber"`
	// Branch is set only on a pre-MR state: the checkout branch the pass reviews
	// against its base. Empty on every PR-scoped state.
	Branch    string            `json:"branch,omitempty"`
	Title     string            `json:"title"`
	TargetSHA string            `json:"targetSha"`
	Status    StateStatus       `json:"status" enum:"needs_review,running,up_to_date,changes_requested,ineligible"`
	LatestRun *domain.ReviewRun `json:"latestRun,omitempty"`
}

// Plan computes per-PR review work from the currently observed PRs and existing
// review runs. It is pure so the trigger path and API list path share exactly
// the same availability/status rules.
func Plan(prs []domain.PullRequest, runs []domain.ReviewRun) []PRReviewState {
	latest := latestRunsByPRAndSHA(runs)
	// Pre-MR runs, keyed on the commit alone. A PR that opens on a commit AO
	// already reviewed before the PR existed must not be reviewed a second time,
	// so a PR with no run of its own adopts the branch run at the same head.
	branch := latestBranchRunsBySHA(runs)
	reviews := make([]PRReviewState, 0, len(prs))
	for _, pr := range prs {
		review := PRReviewState{
			PRURL:     pr.URL,
			PRNumber:  pr.Number,
			Title:     pr.Title,
			TargetSHA: pr.HeadSHA,
			Status:    ReviewStateNeedsReview,
		}
		// A DRAFT PR/MR is eligible. Getting reviewer feedback while the work is
		// still a draft - before it is marked ready - is the point, not an accident:
		// draft state blocks MERGING on both forges, never commenting, so the
		// reviewer's `gh api .../reviews` (event COMMENT) and `glab mr note` post
		// normally. Merged and closed PRs stay ineligible because there is nothing
		// left to act on, and a PR with no URL or no head commit stays ineligible
		// because AO has nothing to review against.
		if pr.URL == "" || pr.HeadSHA == "" || pr.Merged || pr.Closed {
			review.Status = ReviewStateIneligible
			if run, ok := runAtHead(latest, branch, review.PRURL, review.TargetSHA); ok {
				review.LatestRun = &run
			}
			reviews = append(reviews, review)
			continue
		}
		if run, ok := runAtHead(latest, branch, review.PRURL, review.TargetSHA); ok {
			review.LatestRun = &run
			review.Status = runStatus(run)
		}
		reviews = append(reviews, review)
	}
	sort.SliceStable(reviews, func(i, j int) bool {
		if reviews[i].PRNumber != reviews[j].PRNumber {
			return reviews[i].PRNumber < reviews[j].PRNumber
		}
		return reviews[i].PRURL < reviews[j].PRURL
	})
	return reviews
}

// PlanBranch computes the pre-MR review decision for a worker whose checkout has
// no pull request yet: one state keyed on (branch, head sha) instead of
// (PR url, head sha). It applies the same status rules as Plan so the trigger
// path and the API list path agree, and it reads ONLY branch runs (pr_url = "")
// so a post-MR run can never be mistaken for a pre-MR one.
func PlanBranch(branch, headSHA string, runs []domain.ReviewRun) []PRReviewState {
	review := PRReviewState{Branch: branch, TargetSHA: headSHA, Status: ReviewStateNeedsReview}
	latest := latestBranchRunsBySHA(runs)
	if run, ok := latest[headSHA]; ok && headSHA != "" {
		review.LatestRun = &run
	}
	// A checkout with no branch name or no commit gives AO nothing to review
	// against - the same bar Plan holds a PR to (no URL, no head commit).
	if branch == "" || headSHA == "" {
		review.Status = ReviewStateIneligible
		return []PRReviewState{review}
	}
	if review.LatestRun != nil {
		review.Status = runStatus(*review.LatestRun)
	}
	return []PRReviewState{review}
}

// runStatus maps one run to the planning status its PR/branch should show. It is
// the single switch Plan and PlanBranch share.
func runStatus(run domain.ReviewRun) StateStatus {
	switch {
	case run.Status == domain.ReviewRunRunning:
		return ReviewStateRunning
	case run.Verdict == domain.VerdictApproved:
		return ReviewStateUpToDate
	case run.Verdict == domain.VerdictChangesRequested:
		return ReviewStateChangesRequested
	default:
		return ReviewStateNeedsReview
	}
}

// runAtHead returns the run that covers a PR's head: the PR's own run when it has
// one, else the pre-MR run recorded for that same commit before the PR existed.
// The fallback is what stops a commit already reviewed on the branch from being
// reviewed again the moment its PR opens.
func runAtHead(latest, branch map[string]domain.ReviewRun, prURL, targetSHA string) (domain.ReviewRun, bool) {
	if run, ok := latest[prURL+"\x00"+targetSHA]; ok {
		return run, true
	}
	if targetSHA == "" {
		return domain.ReviewRun{}, false
	}
	run, ok := branch[targetSHA]
	return run, ok
}

// latestBranchRunsBySHA indexes the PR-less (pre-MR) runs by the commit they
// reviewed. It is the exact complement of latestRunsByPRAndSHA, which skips them.
func latestBranchRunsBySHA(runs []domain.ReviewRun) map[string]domain.ReviewRun {
	latest := make(map[string]domain.ReviewRun)
	for _, run := range runs {
		if run.PRURL != "" || run.TargetSHA == "" {
			continue
		}
		if existing, ok := latest[run.TargetSHA]; !ok || run.CreatedAt.After(existing.CreatedAt) {
			latest[run.TargetSHA] = run
		}
	}
	return latest
}

func latestRunsByPRAndSHA(runs []domain.ReviewRun) map[string]domain.ReviewRun {
	latest := make(map[string]domain.ReviewRun)
	for _, run := range runs {
		if run.PRURL == "" || run.TargetSHA == "" {
			continue
		}
		key := run.PRURL + "\x00" + run.TargetSHA
		if existing, ok := latest[key]; !ok || run.CreatedAt.After(existing.CreatedAt) {
			latest[key] = run
		}
	}
	return latest
}
