package lifecycle

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A PR stacked on an open parent reports mergeability against its target branch,
// which IS the parent's source branch. A conflict there means the parent gained
// commits the child does not apply on top of, and it stays until the child
// rebases onto the parent: the parent merging does not clear it. So the child's
// worker must be told, whether it owns the parent PR or another worker does.
func TestPRObservation_StackedChildConflictNudges(t *testing.T) {
	const (
		provider = "gitlab"
		host     = "gitlab.example.com"
		repo     = "group/app"
	)
	child := domain.PullRequest{URL: "child", SourceBranch: "feature/readme", TargetBranch: "feature/santa", Provider: provider, Host: host, Repo: repo}
	parent := domain.PullRequest{URL: "parent", SourceBranch: "feature/santa", TargetBranch: "develop", Provider: provider, Host: host, Repo: repo}
	tests := []struct {
		name string
		prs  map[domain.SessionID][]domain.PullRequest
	}{
		{
			name: "parent PR owned by another worker in the same repo",
			prs:  map[domain.SessionID][]domain.PullRequest{"mer-6": {child}, "mer-4": {parent}},
		},
		{
			name: "parent PR owned by the same session",
			prs:  map[domain.SessionID][]domain.PullRequest{"mer-6": {child, parent}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, st, msg := newManager()
			for id, prs := range tc.prs {
				st.sessions[id] = working(id)
				st.prs[id] = prs
			}
			o := ports.PRObservation{Fetched: true, URL: "child", SourceBranch: child.SourceBranch, TargetBranch: child.TargetBranch, Mergeability: domain.MergeConflicting, HeadSHA: "sha-1"}
			if err := m.ApplyPRObservation(ctx, "mer-6", o); err != nil {
				t.Fatal(err)
			}
			if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "merge conflicts") {
				t.Fatalf("stacked child conflict must nudge its worker once, got %v", msg.msgs)
			}
		})
	}
}

// Replays the observation sequence AO recorded for a GitLab MR stacked on another
// worker's open MR (change_log, 2026-11-05 UTC), where the worker was never nudged
// and a human had to say "fix the conflict" by hand. Each conflict episode must
// reach the worker exactly once: re-polls and GitLab's "unknown" recompute window
// stay silent, and a conflict that returns after the worker's fix is news again.
func TestPRObservation_StackedChildConflictEpisodesAcrossWorkers(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-6"] = working("mer-6")
	st.sessions["mer-4"] = working("mer-4")
	child := domain.PullRequest{URL: "child", SourceBranch: "feature/readme", TargetBranch: "feature/santa", Provider: "gitlab", Host: "gitlab.example.com", Repo: "group/app"}
	st.prs["mer-6"] = []domain.PullRequest{child}
	st.prs["mer-4"] = []domain.PullRequest{{URL: "parent", SourceBranch: "feature/santa", TargetBranch: "develop", Provider: "gitlab", Host: "gitlab.example.com", Repo: "group/app"}}

	obs := func(state domain.Mergeability, head string) ports.PRObservation {
		return ports.PRObservation{Fetched: true, URL: "child", SourceBranch: child.SourceBranch, TargetBranch: child.TargetBranch, Mergeability: state, HeadSHA: head}
	}
	steps := []struct {
		at   string
		o    ports.PRObservation
		want int
	}{
		{"02:54:45 parent pushed, child conflicts", obs(domain.MergeConflicting, "a526"), 1},
		{"02:55:15 same conflict re-polled", obs(domain.MergeConflicting, "a526"), 1},
		{"03:00:15 GitLab recompute window", obs(domain.MergeUnknown, "a526"), 1},
		{"03:00:45 same conflict after recompute", obs(domain.MergeConflicting, "a526"), 1},
		{"03:04:45 worker rebased", obs(domain.MergeMergeable, "5e11"), 1},
		{"05:12:15 parent pushed again", obs(domain.MergeUnknown, "5e11"), 1},
		{"05:57:45 conflict returns at the same child head", obs(domain.MergeConflicting, "5e11"), 2},
		{"05:58:15 re-polled", obs(domain.MergeConflicting, "5e11"), 2},
	}
	for _, s := range steps {
		if err := m.ApplyPRObservation(ctx, "mer-6", s.o); err != nil {
			t.Fatalf("%s: %v", s.at, err)
		}
		if len(msg.msgs) != s.want {
			t.Fatalf("%s: want %d merge-conflict nudges, got %d: %v", s.at, s.want, len(msg.msgs), msg.msgs)
		}
	}
}
