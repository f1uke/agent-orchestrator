package controllers

import (
	"testing"

	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// TestNewSessionPRReviewSummaryCarriesApprovalProgress proves the review DTO
// forwards the approval-progress facts (count, required threshold, rule source)
// onto the wire so the display surfaces can render A/T progress.
func TestNewSessionPRReviewSummaryCarriesApprovalProgress(t *testing.T) {
	req := 2
	in := sessionsvc.PRReviewSummary{
		ApprovalsCount:     1,
		RequiredApprovals:  &req,
		ApprovalRuleSource: "ao",
	}
	out := newSessionPRReviewSummary(in)
	if out.ApprovalsCount != 1 {
		t.Fatalf("ApprovalsCount = %d, want 1", out.ApprovalsCount)
	}
	if out.RequiredApprovals == nil || *out.RequiredApprovals != 2 {
		t.Fatalf("RequiredApprovals = %v, want 2", out.RequiredApprovals)
	}
	if out.ApprovalRuleSource != "ao" {
		t.Fatalf("ApprovalRuleSource = %q, want ao", out.ApprovalRuleSource)
	}

	// Unknown threshold stays nil so the surfaces degrade to today's behavior.
	bare := newSessionPRReviewSummary(sessionsvc.PRReviewSummary{ApprovalRuleSource: "none"})
	if bare.RequiredApprovals != nil {
		t.Fatalf("RequiredApprovals = %v, want nil", bare.RequiredApprovals)
	}
}
