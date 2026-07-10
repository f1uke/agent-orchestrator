package domain

// ApprovalRule is a project's rule for when a PR/MR may be reported as
// Ready to merge. It is shaped as an extensible condition set: today it carries
// a single condition — a minimum approval count — but the struct is intended to
// grow (e.g. a CI-must-pass condition) without changing how it is plumbed.
//
// The rule is OFF by default: a project that never opts in keeps the exact
// behavior it had before any approval gating existed. When enabled, its
// conditions are ADDITIVE (AND) with the existing ready-to-merge conditions
// (mergeability, CI, unresolved comments, the SCM approval-rule safety net).
type ApprovalRule struct {
	// Enabled turns the rule on. When false the rule imposes no gate and
	// readiness behaves exactly as if no rule were configured.
	Enabled bool `json:"enabled,omitempty"`
	// Threshold is the minimum number of approvals a PR/MR must carry to be
	// Ready to merge while the rule is enabled. 0 = unset → DefaultApprovalThreshold.
	Threshold int `json:"threshold,omitempty"`
}

// DefaultApprovalThreshold is the approval count an enabled rule requires when
// it configures no explicit threshold.
const DefaultApprovalThreshold = 2

// IsZero reports whether the rule carries no settings, so ProjectConfig.IsZero
// can keep persisting an otherwise-empty config as SQL NULL.
func (r ApprovalRule) IsZero() bool {
	return r == ApprovalRule{}
}

// ResolveThreshold returns the effective approval threshold, defaulting unset or
// non-positive values to DefaultApprovalThreshold.
func (r ApprovalRule) ResolveThreshold() int {
	if r.Threshold <= 0 {
		return DefaultApprovalThreshold
	}
	return r.Threshold
}

// Satisfied reports whether pr meets the rule's approval condition. A disabled
// rule is always satisfied; an enabled rule requires at least ResolveThreshold()
// approvals. This is the only condition modeled today; future conditions AND in
// here.
func (r ApprovalRule) Satisfied(pr PRFacts) bool {
	if !r.Enabled {
		return true
	}
	return pr.ApprovalsCount >= r.ResolveThreshold()
}
