package session

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func TestDecideBranchFollow(t *testing.T) {
	cases := []struct {
		name string
		f    branchFacts
		want BranchFollowOutcome
	}{
		{"on the recorded branch", branchFacts{recorded: "a", head: "a", recordedExists: true}, BranchInSync},
		{"nothing recorded", branchFacts{head: "a"}, BranchInSync},
		{"detached, recorded branch kept", branchFacts{recorded: "a", recordedExists: true}, BranchInSync},
		{"renamed", branchFacts{recorded: "a", head: "b"}, BranchFollowed},
		{"switched, recorded branch kept", branchFacts{recorded: "a", head: "b", recordedExists: true}, BranchLeftSwitched},
		{"recorded branch gone, detached", branchFacts{recorded: "a"}, BranchLeftDetached},
		{"on the target", branchFacts{recorded: "a", head: "develop", protected: []string{"develop"}}, BranchLeftTarget},
		{"on the target, other case", branchFacts{recorded: "a", head: "Develop", protected: []string{"develop"}}, BranchLeftTarget},
		{"another session's branch", branchFacts{recorded: "a", head: "b", owner: "s2"}, BranchLeftOwned},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decideBranchFollow(c.f); got != c.want {
				t.Fatalf("decideBranchFollow(%+v) = %q, want %q", c.f, got, c.want)
			}
		})
	}
}

// followFixture is a repo whose session s1 records feature/ABC-1-fix with the
// worktree on it, and targets develop.
func followFixture(t *testing.T) (*Service, *fakeStore, string) {
	t.Helper()
	dir := t.TempDir()
	runGitIn(t, dir, "init", "-q")
	runGitIn(t, dir, "config", "user.email", "t@example.com")
	runGitIn(t, dir, "config", "user.name", "t")
	runGitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	runGitIn(t, dir, "branch", "-M", "develop")
	runGitIn(t, dir, "checkout", "-qb", "feature/ABC-1-fix")
	runGitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "work")

	st := newFakeStore()
	st.projects["p"] = domain.ProjectRecord{ID: "p", Path: dir, Config: domain.ProjectConfig{DefaultBranch: "develop"}}
	st.sessions["s1"] = domain.SessionRecord{
		ID: "s1", ProjectID: "p", PRTarget: "develop",
		Metadata: domain.SessionMetadata{Branch: "feature/ABC-1-fix", WorkspacePath: dir},
	}
	return &Service{store: st}, st, dir
}

func follow(t *testing.T, svc *Service, st *fakeStore) BranchFollow {
	t.Helper()
	res, err := svc.FollowWorktreeBranch(context.Background(), st.sessions["s1"])
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func recordedBranch(st *fakeStore) string { return st.sessions["s1"].Metadata.Branch }

func TestFollowWorktreeBranch_FollowsARename(t *testing.T) {
	svc, st, dir := followFixture(t)
	runGitIn(t, dir, "branch", "-m", "feature/ABC-2-fix")

	if got := follow(t, svc, st); got.Outcome != BranchFollowed || got.Head != "feature/ABC-2-fix" {
		t.Fatalf("follow = %+v, want followed onto feature/ABC-2-fix", got)
	}
	if got := recordedBranch(st); got != "feature/ABC-2-fix" {
		t.Fatalf("recorded branch = %q, want feature/ABC-2-fix", got)
	}
	if got := follow(t, svc, st); got.Outcome != BranchInSync {
		t.Fatalf("second follow = %+v, want in sync", got)
	}
}

func TestFollowWorktreeBranch_LeavesWhatIsNotARename(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, st *fakeStore, dir string)
		want  BranchFollowOutcome
		owner domain.SessionID
	}{
		{"detached after the branch was deleted", func(t *testing.T, _ *fakeStore, dir string) {
			runGitIn(t, dir, "checkout", "-q", "--detach")
			runGitIn(t, dir, "branch", "-D", "feature/ABC-1-fix")
		}, BranchLeftDetached, ""},
		{"old branch still exists", func(t *testing.T, _ *fakeStore, dir string) {
			runGitIn(t, dir, "checkout", "-qb", "spike/try")
		}, BranchLeftSwitched, ""},
		{"on the target after the branch was deleted", func(t *testing.T, _ *fakeStore, dir string) {
			runGitIn(t, dir, "checkout", "-q", "develop")
			runGitIn(t, dir, "branch", "-D", "feature/ABC-1-fix")
		}, BranchLeftTarget, ""},
		{"renamed onto another live session's branch", func(t *testing.T, st *fakeStore, dir string) {
			st.sessions["s2"] = domain.SessionRecord{ID: "s2", ProjectID: "p",
				Metadata: domain.SessionMetadata{Branch: "feature/ABC-2-fix", WorkspacePath: t.TempDir()}}
			runGitIn(t, dir, "branch", "-m", "feature/ABC-2-fix")
		}, BranchLeftOwned, "s2"},
		{"worktree gone", func(t *testing.T, _ *fakeStore, dir string) {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		}, BranchInSync, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, st, dir := followFixture(t)
			c.setup(t, st, dir)
			got := follow(t, svc, st)
			if got.Outcome != c.want || got.Owner != c.owner {
				t.Fatalf("follow = %+v, want outcome %q owner %q", got, c.want, c.owner)
			}
			if b := recordedBranch(st); b != "feature/ABC-1-fix" {
				t.Fatalf("recorded branch = %q, want it left as feature/ABC-1-fix", b)
			}
		})
	}
}

// A finished session, a queued TODO and a crew member in the same worktree do
// not own a branch against this session.
func TestFollowWorktreeBranch_OnlyLiveSessionsElsewhereOwnABranch(t *testing.T) {
	for name, other := range map[string]domain.SessionRecord{
		"terminated": {IsTerminated: true, Metadata: domain.SessionMetadata{WorkspacePath: "/elsewhere"}},
		"todo":       {IsTodo: true},
		"crew mate":  {},
	} {
		t.Run(name, func(t *testing.T) {
			svc, st, dir := followFixture(t)
			other.ID, other.ProjectID, other.Metadata.Branch = "s2", "p", "feature/ABC-2-fix"
			if other.Metadata.WorkspacePath == "" && !other.IsTodo {
				other.Metadata.WorkspacePath = dir
			}
			st.sessions["s2"] = other
			runGitIn(t, dir, "branch", "-m", "feature/ABC-2-fix")
			if got := follow(t, svc, st); got.Outcome != BranchFollowed {
				t.Fatalf("follow = %+v, want followed", got)
			}
		})
	}
}

// A PR claimed on the old name stays the session's: PRs are tracked by URL, and
// following the rename touches only the branch.
func TestFollowWorktreeBranch_KeepsAClaimedPR(t *testing.T) {
	svc, st, dir := followFixture(t)
	st.prList["s1"] = []domain.PullRequest{{URL: "https://example.com/pr/1", SessionID: "s1", SourceBranch: "feature/ABC-1-fix", TargetBranch: "develop"}}
	runGitIn(t, dir, "branch", "-m", "feature/ABC-2-fix")
	if got := follow(t, svc, st); got.Outcome != BranchFollowed {
		t.Fatalf("follow = %+v, want followed", got)
	}
	prs, _ := st.ListPRsBySession(context.Background(), "s1")
	if len(prs) != 1 || prs[0].URL != "https://example.com/pr/1" {
		t.Fatalf("claimed PRs = %+v, want the old PR still attached", prs)
	}
}

func TestWorkspaceChanges_FollowsARenameWithoutABanner(t *testing.T) {
	svc, st, dir := followFixture(t)
	runGitIn(t, dir, "branch", "-m", "feature/ABC-2-fix")

	res, err := svc.WorkspaceChanges(context.Background(), "s1", WorkspaceChangesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "feature/ABC-2-fix" || res.BranchMissing || res.HeadState != HeadOnBranch || res.DiffSubject != ChangesSubjectBranch {
		t.Fatalf("changes scope = branch %q missing %v head %q subject %q, want the renamed branch measured with HEAD on it",
			res.Branch, res.BranchMissing, res.HeadState, res.DiffSubject)
	}
	if got := recordedBranch(st); got != "feature/ABC-2-fix" {
		t.Fatalf("recorded branch = %q, want the Files read to have followed the rename", got)
	}
}

func TestWorkspaceChanges_StillReportsADeletedBranch(t *testing.T) {
	svc, _, dir := followFixture(t)
	runGitIn(t, dir, "checkout", "-q", "--detach")
	runGitIn(t, dir, "branch", "-D", "feature/ABC-1-fix")

	res, err := svc.WorkspaceChanges(context.Background(), "s1", WorkspaceChangesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.BranchMissing || res.Branch != "feature/ABC-1-fix" {
		t.Fatalf("changes scope = branch %q missing %v, want the deleted branch reported missing", res.Branch, res.BranchMissing)
	}
}

func TestWorkspaceChanges_NamesTheOwnerOfTheWorktreesBranch(t *testing.T) {
	svc, st, dir := followFixture(t)
	st.sessions["s2"] = domain.SessionRecord{ID: "s2", ProjectID: "p",
		Metadata: domain.SessionMetadata{Branch: "feature/ABC-2-fix", WorkspacePath: t.TempDir()}}
	runGitIn(t, dir, "branch", "-m", "feature/ABC-2-fix")

	res, err := svc.WorkspaceChanges(context.Background(), "s1", WorkspaceChangesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.BranchMissing || res.HeadBranchOwner != "s2" {
		t.Fatalf("changes scope = missing %v owner %q, want the old branch missing and s2 named", res.BranchMissing, res.HeadBranchOwner)
	}
}

func TestSetBranch(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, st *fakeStore, dir string)
		branch string
		code   string
		want   string
	}{
		{"a branch that kept its old name", func(t *testing.T, _ *fakeStore, dir string) {
			runGitIn(t, dir, "branch", "feature/ABC-2-fix")
		}, "feature/ABC-2-fix", "", "feature/ABC-2-fix"},
		{"the same branch is a no-op", nil, "feature/ABC-1-fix", "", "feature/ABC-1-fix"},
		{"empty", nil, " ", "BRANCH_REQUIRED", ""},
		{"not in the worktree", nil, "feature/nope", "BRANCH_NOT_IN_WORKTREE", ""},
		{"the target", nil, "develop", "BRANCH_IS_BASE", ""},
		{"another live session's", func(t *testing.T, st *fakeStore, dir string) {
			runGitIn(t, dir, "branch", "feature/ABC-2-fix")
			st.sessions["s2"] = domain.SessionRecord{ID: "s2", ProjectID: "p",
				Metadata: domain.SessionMetadata{Branch: "feature/ABC-2-fix", WorkspacePath: t.TempDir()}}
		}, "feature/ABC-2-fix", "BRANCH_OWNED", ""},
		{"worktree gone", func(t *testing.T, _ *fakeStore, dir string) {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		}, "feature/ABC-2-fix", "WORKSPACE_MISSING", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, st, dir := followFixture(t)
			if c.setup != nil {
				c.setup(t, st, dir)
			}
			_, err := svc.SetBranch(context.Background(), "s1", c.branch)
			if c.code != "" {
				var apiErr *apierr.Error
				if !errors.As(err, &apiErr) || apiErr.Code != c.code {
					t.Fatalf("SetBranch(%q) error = %v, want code %s", c.branch, err, c.code)
				}
				if got := recordedBranch(st); got != "feature/ABC-1-fix" {
					t.Fatalf("recorded branch = %q after a refusal, want it unchanged", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("SetBranch(%q): %v", c.branch, err)
			}
			if got := recordedBranch(st); got != c.want {
				t.Fatalf("recorded branch = %q, want %q", got, c.want)
			}
		})
	}
}
