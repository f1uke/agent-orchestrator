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
		// detailed_merge_status is GitLab's authoritative merge verdict: unlike
		// the coarse merge_status (which returns can_be_merged even when an
		// approval rule is unmet), it names the specific blocker. When present it
		// takes precedence over merge_status.
		{
			// The real nter-ios-app !3028 shape: GitLab still says can_be_merged
			// (conflicts/pipeline are fine) but the approval rule isn't satisfied.
			// detailed_merge_status=not_approved must win, so AO does not call it
			// mergeable / "Ready to merge".
			name:      "not_approved overrides can_be_merged and blocks",
			mr:        restMR{MergeStatus: "can_be_merged", DetailedMergeStatus: "not_approved"},
			wantState: domain.MergeBlocked,
		},
		{
			name:      "detailed mergeable maps to mergeable",
			mr:        restMR{MergeStatus: "can_be_merged", DetailedMergeStatus: "mergeable"},
			wantState: domain.MergeMergeable,
			wantAble:  true,
		},
		{
			name:      "detailed ci_still_running blocks",
			mr:        restMR{MergeStatus: "can_be_merged", DetailedMergeStatus: "ci_still_running"},
			wantState: domain.MergeBlocked,
		},
		{
			name:      "detailed discussions_not_resolved blocks",
			mr:        restMR{MergeStatus: "can_be_merged", DetailedMergeStatus: "discussions_not_resolved"},
			wantState: domain.MergeBlocked,
		},
		{
			name:      "detailed checking maps to unknown",
			mr:        restMR{MergeStatus: "can_be_merged", DetailedMergeStatus: "checking"},
			wantState: domain.MergeUnknown,
		},
		{
			name:      "detailed conflict maps to conflicting",
			mr:        restMR{MergeStatus: "cannot_be_merged", DetailedMergeStatus: "conflict"},
			wantState: domain.MergeConflicting,
			wantConf:  true,
		},
		{
			// has_conflicts still wins even if detailed_merge_status claims mergeable.
			name:      "has_conflicts overrides detailed mergeable",
			mr:        restMR{MergeStatus: "can_be_merged", DetailedMergeStatus: "mergeable", HasConflicts: true},
			wantState: domain.MergeConflicting,
			wantConf:  true,
		},
		{
			// Older GitLab (<15.6) omits detailed_merge_status: fall back to the
			// legacy merge_status mapping unchanged.
			name:      "empty detailed falls back to can_be_merged",
			mr:        restMR{MergeStatus: "can_be_merged", DetailedMergeStatus: ""},
			wantState: domain.MergeMergeable,
			wantAble:  true,
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
