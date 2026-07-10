package domain

import "testing"

func TestApprovalRuleResolveThreshold(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"unset defaults to 2", 0, 2},
		{"negative defaults to 2", -3, 2},
		{"explicit 1", 1, 1},
		{"explicit 5", 5, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := (ApprovalRule{Threshold: c.in}).ResolveThreshold(); got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestApprovalRuleDefaultThresholdIsTwo(t *testing.T) {
	if DefaultApprovalThreshold != 2 {
		t.Fatalf("DefaultApprovalThreshold = %d, want 2", DefaultApprovalThreshold)
	}
}

func TestApprovalRuleSatisfied(t *testing.T) {
	cases := []struct {
		name  string
		rule  ApprovalRule
		count int
		want  bool
	}{
		{"disabled is always satisfied (0 approvals)", ApprovalRule{Enabled: false}, 0, true},
		{"disabled is always satisfied (ignores threshold)", ApprovalRule{Enabled: false, Threshold: 5}, 0, true},
		{"enabled default threshold, under", ApprovalRule{Enabled: true}, 1, false},
		{"enabled default threshold, met", ApprovalRule{Enabled: true}, 2, true},
		{"enabled default threshold, over", ApprovalRule{Enabled: true}, 3, true},
		{"enabled explicit threshold, under", ApprovalRule{Enabled: true, Threshold: 3}, 2, false},
		{"enabled explicit threshold, met", ApprovalRule{Enabled: true, Threshold: 3}, 3, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.rule.Satisfied(PRFacts{ApprovalsCount: c.count})
			if got != c.want {
				t.Fatalf("Satisfied(count=%d) = %v, want %v", c.count, got, c.want)
			}
		})
	}
}

func TestApprovalRuleIsZero(t *testing.T) {
	if !(ApprovalRule{}).IsZero() {
		t.Fatal("empty rule should be zero")
	}
	if (ApprovalRule{Enabled: true}).IsZero() {
		t.Fatal("enabled rule should not be zero")
	}
	if (ApprovalRule{Threshold: 2}).IsZero() {
		t.Fatal("rule with threshold should not be zero")
	}
}
