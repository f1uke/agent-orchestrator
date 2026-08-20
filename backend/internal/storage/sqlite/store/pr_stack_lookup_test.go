package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ListOpenPRSourceBranchesInRepo is the real-SQLite read lifecycle uses to spot a
// stacked PR whose parent belongs to a DIFFERENT worker. It must reach across the
// project's sessions (that is the whole point) while staying pinned to one
// repository and one project, because branch names are not unique on their own.
func TestListOpenPRSourceBranchesInRepoSpansSessionsButNotRepos(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	seedProject(t, s, "oth")
	a, _ := s.CreateSession(ctx, sampleRecord("mer"))
	b, _ := s.CreateSession(ctx, sampleRecord("mer"))
	other, _ := s.CreateSession(ctx, sampleRecord("oth"))
	now := time.Now().UTC().Truncate(time.Second)

	write := func(pr domain.PullRequest) {
		t.Helper()
		pr.UpdatedAt, pr.ObservedAt = now, now
		if pr.Provider == "" {
			pr.Provider, pr.Host, pr.Repo = "github", "github.com", "o/r"
		}
		if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
			t.Fatalf("write %s: %v", pr.URL, err)
		}
	}
	write(domain.PullRequest{URL: "a-root", SessionID: a.ID, Number: 1, SourceBranch: "ao/x", TargetBranch: "main"})
	write(domain.PullRequest{URL: "b-child", SessionID: b.ID, Number: 2, SourceBranch: "ao/x/auth", TargetBranch: "ao/x"})
	write(domain.PullRequest{URL: "a-merged", SessionID: a.ID, Number: 3, Merged: true, SourceBranch: "ao/done", TargetBranch: "main"})
	write(domain.PullRequest{URL: "a-closed", SessionID: a.ID, Number: 4, Closed: true, SourceBranch: "ao/dropped", TargetBranch: "main"})
	// Same branch name, different repository and different project: neither may
	// leak into the answer.
	write(domain.PullRequest{URL: "elsewhere-repo", SessionID: a.ID, Number: 5, SourceBranch: "ao/x", TargetBranch: "main", Provider: "github", Host: "github.com", Repo: "o/other"})
	write(domain.PullRequest{URL: "elsewhere-project", SessionID: other.ID, Number: 6, SourceBranch: "ao/x", TargetBranch: "main"})

	got, err := s.ListOpenPRSourceBranchesInRepo(ctx, "mer", "github", "github.com", "o/r")
	if err != nil {
		t.Fatal(err)
	}
	// Open PRs only, both sessions, sorted; "ao/x" appears once even though the
	// other repo and the other project also have a branch by that name.
	want := []string{"ao/x", "ao/x/auth"}
	if len(got) != len(want) {
		t.Fatalf("branches = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("branches = %v, want %v", got, want)
		}
	}

	// A repository the project has no PRs in returns empty, never an error.
	none, err := s.ListOpenPRSourceBranchesInRepo(ctx, "mer", "github", "github.com", "o/unseen")
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("unseen repo = %v, want empty", none)
	}
}
