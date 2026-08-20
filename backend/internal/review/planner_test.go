package review

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestPlanStatuses(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	tests := []struct {
		name string
		pr   domain.PullRequest
		runs []domain.ReviewRun
		want StateStatus
	}{
		{name: "open needs review", pr: planPR("pr1", 1, "sha1"), want: ReviewStateNeedsReview},
		// Reviewing BEFORE the PR is marked ready is the point of the feature: the
		// human wants reviewer feedback while the work is still a draft. Draft-ness
		// blocks merging on both forges, never reviewing.
		{name: "github draft needs review", pr: withDraft(planPR("https://github.com/o/r/pull/1", 1, "sha1")), want: ReviewStateNeedsReview},
		{name: "gitlab draft mr needs review", pr: withDraft(planPR("https://gitlab.example.com/g/p/-/merge_requests/7", 7, "sha7")), want: ReviewStateNeedsReview},
		// A draft that already has a verdict for its current head still reports that
		// verdict - eligibility must not override an existing run's outcome.
		{name: "approved draft up to date", pr: withDraft(planPR("pr1", 1, "sha1")), runs: []domain.ReviewRun{
			{ID: "run-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, CreatedAt: now},
		}, want: ReviewStateUpToDate},
		// Not widened beyond draft: everything else that was ineligible stays so.
		{name: "merged ineligible", pr: withMerged(planPR("pr1", 1, "sha1")), want: ReviewStateIneligible},
		{name: "closed ineligible", pr: withClosed(planPR("pr1", 1, "sha1")), want: ReviewStateIneligible},
		{name: "draft and merged still ineligible", pr: withMerged(withDraft(planPR("pr1", 1, "sha1"))), want: ReviewStateIneligible},
		{name: "missing url ineligible", pr: planPR("", 1, "sha1"), want: ReviewStateIneligible},
		{name: "missing head sha ineligible", pr: planPR("pr1", 1, ""), want: ReviewStateIneligible},
		{name: "draft missing head sha ineligible", pr: withDraft(planPR("pr1", 1, "")), want: ReviewStateIneligible},
		{name: "approved current sha up to date", pr: planPR("pr1", 1, "sha1"), runs: []domain.ReviewRun{
			{ID: "run-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, CreatedAt: now},
		}, want: ReviewStateUpToDate},
		{name: "changes requested current sha", pr: planPR("pr1", 1, "sha1"), runs: []domain.ReviewRun{
			{ID: "run-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested, CreatedAt: now},
		}, want: ReviewStateChangesRequested},
		{name: "running current sha", pr: planPR("pr1", 1, "sha1"), runs: []domain.ReviewRun{
			{ID: "run-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning, CreatedAt: now},
		}, want: ReviewStateRunning},
		{name: "different sha needs review", pr: planPR("pr1", 1, "sha2"), runs: []domain.ReviewRun{
			{ID: "run-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, CreatedAt: now},
		}, want: ReviewStateNeedsReview},
		{name: "failed current sha retryable", pr: planPR("pr1", 1, "sha1"), runs: []domain.ReviewRun{
			{ID: "run-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunFailed, CreatedAt: now},
		}, want: ReviewStateNeedsReview},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Plan([]domain.PullRequest{tt.pr}, tt.runs)
			if len(got) != 1 {
				t.Fatalf("review states = %d, want 1", len(got))
			}
			if got[0].Status != tt.want {
				t.Fatalf("status = %s, want %s; item=%+v", got[0].Status, tt.want, got[0])
			}
		})
	}
}

func planPR(url string, n int, sha string) domain.PullRequest {
	return domain.PullRequest{URL: url, Number: n, HeadSHA: sha}
}

func withDraft(pr domain.PullRequest) domain.PullRequest {
	pr.Draft = true
	return pr
}

func withMerged(pr domain.PullRequest) domain.PullRequest {
	pr.Merged = true
	return pr
}

func withClosed(pr domain.PullRequest) domain.PullRequest {
	pr.Closed = true
	return pr
}

// PlanBranch is the pre-MR half of the planner: same status rules as Plan, keyed
// on (branch, head sha) instead of (PR url, head sha).
func TestPlanBranchStatuses(t *testing.T) {
	head := "sha1"
	tests := []struct {
		name   string
		branch string
		sha    string
		runs   []domain.ReviewRun
		want   StateStatus
	}{
		{"never reviewed", "feature/x", head, nil, ReviewStateNeedsReview},
		{"running", "feature/x", head, []domain.ReviewRun{{TargetSHA: head, Status: domain.ReviewRunRunning}}, ReviewStateRunning},
		{"approved", "feature/x", head, []domain.ReviewRun{{TargetSHA: head, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}}, ReviewStateUpToDate},
		{"changes requested", "feature/x", head, []domain.ReviewRun{{TargetSHA: head, Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested}}, ReviewStateChangesRequested},
		{"failed retries", "feature/x", head, []domain.ReviewRun{{TargetSHA: head, Status: domain.ReviewRunFailed}}, ReviewStateNeedsReview},
		{"other commit does not count", "feature/x", head, []domain.ReviewRun{{TargetSHA: "sha0", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}}, ReviewStateNeedsReview},
		// A PR-scoped run is never read as a branch run: the two keys are disjoint.
		{"pr run does not count", "feature/x", head, []domain.ReviewRun{{PRURL: "pr1", TargetSHA: head, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}}, ReviewStateNeedsReview},
		{"no branch", "", head, nil, ReviewStateIneligible},
		{"no commit", "feature/x", "", nil, ReviewStateIneligible},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PlanBranch(tt.branch, tt.sha, tt.runs)
			if len(got) != 1 {
				t.Fatalf("want exactly one branch state, got %d", len(got))
			}
			if got[0].Status != tt.want {
				t.Fatalf("status = %q, want %q", got[0].Status, tt.want)
			}
			if got[0].PRURL != "" || got[0].PRNumber != 0 {
				t.Fatalf("a branch state must carry no PR identity: %+v", got[0])
			}
		})
	}
}

// A PR that opens on a commit already reviewed before it existed adopts that
// verdict, so the same commit is never reviewed twice.
func TestPlanAdoptsAPreMRRunAtThePRHead(t *testing.T) {
	prs := []domain.PullRequest{{URL: "pr1", Number: 1, HeadSHA: "sha1"}}
	runs := []domain.ReviewRun{{ID: "run-1", PRURL: "", TargetSHA: "sha1", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}}

	got := Plan(prs, runs)
	if len(got) != 1 || got[0].Status != ReviewStateUpToDate {
		t.Fatalf("plan = %+v; want up_to_date from the adopted pre-MR run", got)
	}
	if got[0].LatestRun == nil || got[0].LatestRun.ID != "run-1" {
		t.Fatalf("latest run = %+v; want the pre-MR run", got[0].LatestRun)
	}
}

// The PR's own run always wins over an adopted one, so a re-review after
// feedback is not masked by the older pre-MR verdict.
func TestPlanPrefersThePRsOwnRunOverAPreMROne(t *testing.T) {
	prs := []domain.PullRequest{{URL: "pr1", Number: 1, HeadSHA: "sha1"}}
	runs := []domain.ReviewRun{
		{ID: "pre", PRURL: "", TargetSHA: "sha1", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved},
		{ID: "post", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested},
	}

	got := Plan(prs, runs)
	if got[0].LatestRun == nil || got[0].LatestRun.ID != "post" {
		t.Fatalf("latest run = %+v; want the PR-scoped run", got[0].LatestRun)
	}
	if got[0].Status != ReviewStateChangesRequested {
		t.Fatalf("status = %q", got[0].Status)
	}
}
