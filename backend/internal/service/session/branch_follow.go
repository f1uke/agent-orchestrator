package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// BranchFollowOutcome is what AO concluded on comparing the branch a session
// records with the branch its worktree is on.
type BranchFollowOutcome string

const (
	// BranchInSync: nothing to follow. The worktree is on the recorded branch,
	// or there is no worktree, recorded branch or comparable repo to read.
	BranchInSync BranchFollowOutcome = "in_sync"
	// BranchFollowed: the recorded branch was renamed in the worktree and the
	// session now records the new name.
	BranchFollowed BranchFollowOutcome = "followed"
	// BranchLeftDetached: the recorded branch is gone and HEAD is detached, so
	// there is no name to adopt.
	BranchLeftDetached BranchFollowOutcome = "detached"
	// BranchLeftSwitched: the recorded branch still exists and the worktree is on
	// another one. That is a checkout, not a rename (a baseline install, the next
	// PR's branch), and the session keeps the branch it owns.
	BranchLeftSwitched BranchFollowOutcome = "switched"
	// BranchLeftTarget: the worktree is on a branch the session merges into or
	// was cut from, which can never be the session's own.
	BranchLeftTarget BranchFollowOutcome = "target"
	// BranchLeftOwned: the worktree is on a branch another live session records.
	// Taking it would leave two sessions claiming one branch's PRs.
	BranchLeftOwned BranchFollowOutcome = "owned"
)

// BranchFollow is one comparison's result. Head is the worktree's branch ("" when
// detached) and Owner the live session that records Head, when one does.
type BranchFollow struct {
	Outcome  BranchFollowOutcome
	Recorded string
	Head     string
	Owner    domain.SessionID
}

// branchFacts is what decideBranchFollow needs, read from git and the store.
type branchFacts struct {
	recorded       string
	head           string
	recordedExists bool
	protected      []string
	owner          domain.SessionID
}

// decideBranchFollow adopts the worktree's branch only when the move is an
// unambiguous rename: the recorded branch no longer exists, the worktree is on a
// named branch, and that branch is neither a base the session merges into nor
// another live session's.
func decideBranchFollow(f branchFacts) BranchFollowOutcome {
	switch {
	case f.recorded == "" || f.head == f.recorded:
		return BranchInSync
	case f.recordedExists && f.head == "":
		return BranchInSync
	case f.recordedExists:
		return BranchLeftSwitched
	case f.head == "":
		return BranchLeftDetached
	case containsFold(f.protected, f.head):
		return BranchLeftTarget
	case f.owner != "":
		return BranchLeftOwned
	default:
		return BranchFollowed
	}
}

// FollowWorktreeBranch moves rec onto the branch its worktree is on when the
// recorded branch was renamed there, and reports what it found. It is called by
// the follow loop for every live session and before every Files read, and is
// idempotent: a session already in sync costs one git call and writes nothing.
func (s *Service) FollowWorktreeBranch(ctx context.Context, rec domain.SessionRecord) (BranchFollow, error) {
	res := BranchFollow{Outcome: BranchInSync, Recorded: strings.TrimSpace(rec.Metadata.Branch)}
	workspace := rec.Metadata.WorkspacePath
	if res.Recorded == "" || workspace == "" || !isDir(workspace) {
		return res, nil
	}
	res.Head, _ = readHead(ctx, workspace)
	if res.Head == res.Recorded {
		return res, nil
	}
	project, _, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return res, err
	}
	// A workspace project's branch spans the root and every child repo; renaming
	// the root alone does not move the children, so there is no single new name.
	if project.Kind.WithDefault() == domain.ProjectKindWorkspace {
		return res, nil
	}
	facts := branchFacts{recorded: res.Recorded, head: res.Head}
	_, facts.recordedExists = resolveLocalBranchRef(ctx, workspace, res.Recorded)
	if !facts.recordedExists && res.Head != "" {
		facts.protected = s.sessionBaseBranches(ctx, rec, project)
		if facts.owner, err = s.branchOwner(ctx, rec, res.Head); err != nil {
			return res, err
		}
	}
	res.Owner = facts.owner
	res.Outcome = decideBranchFollow(facts)
	if res.Outcome != BranchFollowed {
		return res, nil
	}
	if _, err := s.store.SetSessionBranch(ctx, rec.ID, res.Recorded, res.Head, s.now()); err != nil {
		return res, err
	}
	return res, nil
}

// SetBranch records branch as the session's own, the explicit form of
// FollowWorktreeBranch for what it leaves alone (a rename that kept the old
// branch, a detached worktree being pointed back at a branch). The branch must
// exist in the session's worktree and must not be a base the session merges into
// or another live session's.
func (s *Service) SetBranch(ctx context.Context, id domain.SessionID, branch string) (domain.Session, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return domain.Session{}, apierr.Invalid("BRANCH_REQUIRED", "Branch is required", nil)
	}
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}
	if !ok {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	workspace := rec.Metadata.WorkspacePath
	if workspace == "" || !isDir(workspace) {
		return domain.Session{}, apierr.Conflict("WORKSPACE_MISSING",
			fmt.Sprintf("Session %s has no worktree on disk to check branch %q against", id, branch), nil)
	}
	if _, ok := resolveLocalBranchRef(ctx, workspace, branch); !ok {
		return domain.Session{}, apierr.Invalid("BRANCH_NOT_IN_WORKTREE",
			fmt.Sprintf("Branch %q does not exist in the worktree at %s", branch, workspace), nil)
	}
	project, _, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return domain.Session{}, err
	}
	if containsFold(s.sessionBaseBranches(ctx, rec, project), branch) {
		return domain.Session{}, apierr.Invalid("BRANCH_IS_BASE",
			fmt.Sprintf("Branch %q is what session %s merges into or was cut from, not its own branch", branch, id), nil)
	}
	owner, err := s.branchOwner(ctx, rec, branch)
	if err != nil {
		return domain.Session{}, err
	}
	if owner != "" {
		return domain.Session{}, apierr.Conflict("BRANCH_OWNED",
			fmt.Sprintf("Branch %q belongs to live session %s", branch, owner), map[string]any{"owner": owner})
	}
	if branch != rec.Metadata.Branch {
		updated, err := s.store.SetSessionBranch(ctx, id, rec.Metadata.Branch, branch, s.now())
		if err != nil {
			return domain.Session{}, err
		}
		if !updated {
			return domain.Session{}, apierr.Conflict("BRANCH_CHANGED",
				fmt.Sprintf("Session %s's branch changed while it was being set; run the command again", id), nil)
		}
	}
	return s.Get(ctx, id)
}

// sessionBaseBranches are the branches a session merges into or was cut from:
// never its own.
func (s *Service) sessionBaseBranches(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) []string {
	out := []string{rec.PRTarget, rec.BaseBranch, project.Config.WithDefaults().DefaultBranch}
	if prs, err := s.store.ListPRsBySession(ctx, rec.ID); err == nil {
		for _, p := range prs {
			out = append(out, p.TargetBranch)
		}
	}
	return out
}

// branchOwner is the live session, other than rec, that records branch. A crew
// member shares rec's worktree and with it the branch, so a session on the same
// worktree is never "another" owner.
func (s *Service) branchOwner(ctx context.Context, rec domain.SessionRecord, branch string) (domain.SessionID, error) {
	sessions, err := s.store.ListSessions(ctx, rec.ProjectID)
	if err != nil {
		return "", err
	}
	for _, other := range sessions {
		if other.ID == rec.ID || other.IsTerminated || other.IsTodo || other.Metadata.WorkspacePath == rec.Metadata.WorkspacePath {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(other.Metadata.Branch), branch) {
			return other.ID, nil
		}
	}
	return "", nil
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if v = strings.TrimSpace(v); v != "" && strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
