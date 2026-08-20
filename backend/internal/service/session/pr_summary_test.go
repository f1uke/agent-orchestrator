package session

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestSummarizeReviewApprovalProgress covers the approval-progress resolution
// feeding the display surfaces: approved count, effective required threshold,
// and which rule set it (scm / ao / none). See the design data contract §7.
func TestSummarizeReviewApprovalProgress(t *testing.T) {
	ptr := func(n int) *int { return &n }
	cases := []struct {
		name         string
		pr           domain.PullRequest
		rule         domain.ApprovalRule
		wantCount    int
		wantRequired *int
		wantSource   string
	}{
		{
			name:         "scm rule with known required count",
			pr:           domain.PullRequest{ApprovalsCount: 1, ApprovalsRequired: 2, ApprovalRuleConfigured: true},
			rule:         domain.ApprovalRule{Enabled: true, Threshold: 5},
			wantCount:    1,
			wantRequired: ptr(2),
			wantSource:   "scm",
		},
		{
			name:         "scm rule without a numeric required count degrades to count-only",
			pr:           domain.PullRequest{ApprovalsCount: 3, ApprovalsRequired: 0, ApprovalRuleConfigured: true},
			rule:         domain.ApprovalRule{Enabled: true, Threshold: 2},
			wantCount:    3,
			wantRequired: nil,
			wantSource:   "scm",
		},
		{
			name:         "ao additive rule sets the threshold when the scm has none",
			pr:           domain.PullRequest{Provider: "gitlab", ApprovalsCount: 1, ApprovalRuleConfigured: false},
			rule:         domain.ApprovalRule{Enabled: true, Threshold: 3},
			wantCount:    1,
			wantRequired: ptr(3),
			wantSource:   "ao",
		},
		{
			name:         "ao rule with unset threshold defaults to two",
			pr:           domain.PullRequest{Provider: "gitlab", ApprovalsCount: 0, ApprovalRuleConfigured: false},
			rule:         domain.ApprovalRule{Enabled: true},
			wantCount:    0,
			wantRequired: ptr(domain.DefaultApprovalThreshold),
			wantSource:   "ao",
		},
		{
			// GitHub does not report approval counts, so an AO count-based rule
			// cannot be surfaced as progress there — it would show a misleading
			// 0/T that contradicts GitHub's own approved decision. Degrade to none.
			name:         "ao rule on a provider that reports no counts degrades to none",
			pr:           domain.PullRequest{Provider: "github", ApprovalsCount: 0, ApprovalRuleConfigured: false},
			rule:         domain.ApprovalRule{Enabled: true, Threshold: 2},
			wantCount:    0,
			wantRequired: nil,
			wantSource:   "none",
		},
		{
			name:         "no rule anywhere leaves the threshold unknown",
			pr:           domain.PullRequest{Provider: "gitlab", ApprovalsCount: 1, ApprovalRuleConfigured: false},
			rule:         domain.ApprovalRule{Enabled: false},
			wantCount:    1,
			wantRequired: nil,
			wantSource:   "none",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := summarizeReview(c.pr, nil, nil, c.rule)
			if got.ApprovalsCount != c.wantCount {
				t.Fatalf("ApprovalsCount = %d, want %d", got.ApprovalsCount, c.wantCount)
			}
			if c.wantRequired == nil {
				if got.RequiredApprovals != nil {
					t.Fatalf("RequiredApprovals = %d, want nil", *got.RequiredApprovals)
				}
			} else if got.RequiredApprovals == nil || *got.RequiredApprovals != *c.wantRequired {
				t.Fatalf("RequiredApprovals = %v, want %d", got.RequiredApprovals, *c.wantRequired)
			}
			if got.ApprovalRuleSource != c.wantSource {
				t.Fatalf("ApprovalRuleSource = %q, want %q", got.ApprovalRuleSource, c.wantSource)
			}
		})
	}
}

// TestSummarizeReviewApprovalProgressMergedOmitted proves a merged PR carries no
// approval-progress (row J degrades to today's behavior).
func TestSummarizeReviewApprovalProgressMergedOmitted(t *testing.T) {
	pr := domain.PullRequest{Merged: true, ApprovalsCount: 2, ApprovalsRequired: 2, ApprovalRuleConfigured: true}
	got := summarizeReview(pr, nil, nil, domain.ApprovalRule{Enabled: true})
	if got.RequiredApprovals != nil || got.ApprovalRuleSource != "" || got.ApprovalsCount != 0 {
		t.Fatalf("merged PR should carry no approval progress, got %+v", got)
	}
}

// --- AO's own verdict on the PR read model ---
//
// Before this, an AO approval reached nobody: ApplyReviewResult treated it as a
// no-op and the board read only the provider's decision, so the durable
// review_run row was invisible everywhere a human looks. These pin the surface
// that fixed it.

func TestListPRSummariesCarriesAOApprovalAtHead(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
	st.prList["mer-1"] = []domain.PullRequest{{URL: "pr1", Number: 1, HeadSHA: "sha1"}}
	st.reviewRuns["mer-1"] = []domain.ReviewRun{{
		ID: "run-1", SessionID: "mer-1", PRURL: "pr1", TargetSHA: "sha1",
		Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, Body: "looks good",
	}}

	out, err := (&Service{store: st}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("ListPRSummaries: %v", err)
	}
	if len(out) != 1 || out[0].AOReview == nil {
		t.Fatalf("AO's approval did not reach the PR read model: %+v", out)
	}
	got := out[0].AOReview
	if got.Verdict != domain.VerdictApproved || got.RunID != "run-1" || got.TargetSHA != "sha1" || got.PreMR {
		t.Fatalf("aoReview = %+v", got)
	}
	// AO is a separate actor: it must not be laundered into the provider's decision.
	if out[0].Review.Decision == domain.ReviewApproved {
		t.Fatalf("AO's verdict must not rewrite the provider decision: %+v", out[0].Review)
	}
}

// A verdict on a commit the PR has moved past says nothing about what is on it
// now. Reporting it would be the same mistake as calling an unreviewed PR
// reviewed.
func TestListPRSummariesIgnoresAnAOVerdictOnAnOlderCommit(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
	st.prList["mer-1"] = []domain.PullRequest{{URL: "pr1", Number: 1, HeadSHA: "sha2"}}
	st.reviewRuns["mer-1"] = []domain.ReviewRun{{
		ID: "run-1", SessionID: "mer-1", PRURL: "pr1", TargetSHA: "sha1",
		Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved,
	}}

	out, err := (&Service{store: st}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("ListPRSummaries: %v", err)
	}
	if out[0].AOReview != nil {
		t.Fatalf("a stale verdict must not be reported: %+v", out[0].AOReview)
	}
}

// A pre-MR pass reviewed this exact tree before the PR existed, so it counts —
// but it is marked, because there is no posted review behind it to open.
func TestListPRSummariesAdoptsAPreMRVerdictAndMarksIt(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
	st.prList["mer-1"] = []domain.PullRequest{{URL: "pr1", Number: 1, HeadSHA: "sha1"}}
	st.reviewRuns["mer-1"] = []domain.ReviewRun{{
		ID: "run-pre", SessionID: "mer-1", PRURL: "", TargetSHA: "sha1",
		Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved,
	}}

	out, err := (&Service{store: st}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("ListPRSummaries: %v", err)
	}
	if out[0].AOReview == nil || !out[0].AOReview.PreMR || out[0].AOReview.RunID != "run-pre" {
		t.Fatalf("aoReview = %+v; want the adopted pre-MR verdict, marked", out[0].AOReview)
	}
}

// Another PR's verdict at a coincidentally equal head must not leak across.
func TestListPRSummariesDoesNotBorrowAnotherPRsVerdict(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
	st.prList["mer-1"] = []domain.PullRequest{{URL: "pr1", Number: 1, HeadSHA: "sha1"}}
	st.reviewRuns["mer-1"] = []domain.ReviewRun{{
		ID: "run-other", SessionID: "mer-1", PRURL: "pr2", TargetSHA: "sha1",
		Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved,
	}}

	out, err := (&Service{store: st}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("ListPRSummaries: %v", err)
	}
	if out[0].AOReview != nil {
		t.Fatalf("another PR's verdict leaked: %+v", out[0].AOReview)
	}
}

// A pass still running has no verdict yet, and neither does a failed one.
func TestListPRSummariesReportsNoVerdictForARunningOrFailedPass(t *testing.T) {
	for _, run := range []domain.ReviewRun{
		{ID: "r", SessionID: "mer-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning},
		{ID: "r", SessionID: "mer-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunFailed},
	} {
		st := newFakeStore()
		st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
		st.prList["mer-1"] = []domain.PullRequest{{URL: "pr1", Number: 1, HeadSHA: "sha1"}}
		st.reviewRuns["mer-1"] = []domain.ReviewRun{run}

		out, err := (&Service{store: st}).ListPRSummaries(context.Background(), "mer-1")
		if err != nil {
			t.Fatalf("ListPRSummaries: %v", err)
		}
		if out[0].AOReview != nil {
			t.Fatalf("%s run should carry no verdict: %+v", run.Status, out[0].AOReview)
		}
	}
}
