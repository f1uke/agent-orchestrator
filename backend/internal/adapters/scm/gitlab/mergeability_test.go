package gitlab

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestMergeabilityNormalizesToDomainEnum guards that GitLab's raw
// merge_status vocabulary is normalized onto AO's domain.Mergeability enum,
// the same contract the github adapter honors. The observer casts
// SCMMergeabilityObservation.State straight into domain.Mergeability, and the
// status pipeline only treats an MR as "Ready to merge" when that value equals
// domain.MergeMergeable ("mergeable"). Emitting GitLab's raw "can_be_merged"
// silently strands mergeable MRs in the "In review" column.
func TestMergeabilityNormalizesToDomainEnum(t *testing.T) {
	cases := []struct {
		name      string
		mr        restMR
		wantState domain.Mergeability
		wantAble  bool
		wantConf  bool
	}{
		{
			name:      "can_be_merged maps to mergeable",
			mr:        restMR{MergeStatus: "can_be_merged"},
			wantState: domain.MergeMergeable,
			wantAble:  true,
		},
		{
			name:      "conflicts map to conflicting",
			mr:        restMR{MergeStatus: "cannot_be_merged", HasConflicts: true},
			wantState: domain.MergeConflicting,
			wantConf:  true,
		},
		{
			name:      "cannot_be_merged without conflicts is blocked",
			mr:        restMR{MergeStatus: "cannot_be_merged"},
			wantState: domain.MergeBlocked,
		},
		{
			name:      "unchecked maps to unknown",
			mr:        restMR{MergeStatus: "unchecked"},
			wantState: domain.MergeUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeability(tc.mr)
			if domain.Mergeability(got.State) != tc.wantState {
				t.Errorf("State = %q, want %q", got.State, tc.wantState)
			}
			if got.Mergeable != tc.wantAble {
				t.Errorf("Mergeable = %v, want %v", got.Mergeable, tc.wantAble)
			}
			if got.Conflict != tc.wantConf {
				t.Errorf("Conflict = %v, want %v", got.Conflict, tc.wantConf)
			}
		})
	}
}
