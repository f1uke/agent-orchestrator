package session

import (
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
