package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Errors surfaced by SetTargetBranch. Each names a cause the human can act on,
// which is the whole point: before this, a target branch that did not exist
// reached them as a 503 "SCM unavailable" — blaming the service for bad input
// and inviting a retry that could never succeed.
var (
	// ErrTargetBranchRequired is a target that is empty or only whitespace.
	ErrTargetBranchRequired = errors.New("target branch is required")
	// ErrTargetBranchNotFound is a target that does not exist on the remote.
	ErrTargetBranchNotFound = errors.New("target branch does not exist on the remote")
	// ErrRetargetRefused is the forge declining the change on its merits — the
	// PR is already merged or closed, or the target equals the source branch.
	ErrRetargetRefused = errors.New("the pull request could not be retargeted")
	// ErrRetargetUnsupported is a provider with no retarget capability.
	ErrRetargetUnsupported = errors.New("this provider does not support retargeting")
)

// SetTargetBranch records the branch this session's work merges into, and — when
// the session owns an OPEN pull/merge request — retargets that request on the
// forge first.
//
// Order is the contract, not an implementation detail:
//
//  1. reject an empty target;
//  2. if the open PR is already on this target, do nothing outbound and just
//     reconcile the stored value (idempotent: a no-op, never an error);
//  3. confirm the branch exists on the remote — retargeting onto a branch that
//     is not there is worse than refusing;
//  4. write to the forge;
//  5. ONLY on success, persist locally.
//
// Because the local write is last, a failed retarget leaves AO's stored value
// untouched. AO and the forge cannot end up claiming different targets — the
// divergence is structurally impossible rather than merely detected and
// reported afterwards.
func (s *Service) SetTargetBranch(ctx context.Context, id domain.SessionID, target string) (domain.Session, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return domain.Session{}, fmt.Errorf("%w: %w", ErrTargetBranchRequired,
			apierr.Invalid("TARGET_BRANCH_REQUIRED", "Target branch is required", nil))
	}

	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}
	if !ok {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}

	openPR, hasOpenPR, err := s.openPRForSession(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}

	if hasOpenPR && !strings.EqualFold(strings.TrimSpace(openPR.TargetBranch), target) {
		if err := s.retargetOnSCM(ctx, rec, openPR, target); err != nil {
			// Deliberately before any local write: nothing is persisted.
			return domain.Session{}, err
		}
		// Move the tracked PR row too. An open PR outranks the session's stored
		// target in resolveTargetChain, so leaving this stale would render the
		// successful edit as though it had not taken effect until the observer
		// next polls.
		if _, err := s.store.SetPRTargetBranch(ctx, openPR.URL, target, s.now()); err != nil {
			return domain.Session{}, err
		}
	}

	if _, err := s.store.SetSessionPRTarget(ctx, id, target, s.now()); err != nil {
		return domain.Session{}, err
	}
	return s.Get(ctx, id)
}

// openPRForSession returns the session's first still-open pull request.
func (s *Service) openPRForSession(ctx context.Context, id domain.SessionID) (domain.PullRequest, bool, error) {
	prs, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return domain.PullRequest{}, false, err
	}
	for _, p := range prs {
		if !p.Merged && !p.Closed {
			return p, true, nil
		}
	}
	return domain.PullRequest{}, false, nil
}

// retargetOnSCM validates the branch and performs the outbound write.
func (s *Service) retargetOnSCM(ctx context.Context, rec domain.SessionRecord, pr domain.PullRequest, target string) error {
	if s.scm == nil {
		return ErrSCMUnavailable
	}
	retargeter, ok := s.scm.(scmRetargeter)
	if !ok {
		return fmt.Errorf("%w: %w", ErrRetargetUnsupported,
			apierr.Invalid("RETARGET_UNSUPPORTED", "This provider does not support retargeting", nil))
	}

	var origin string
	if proj, ok, err := s.store.GetProject(ctx, string(rec.ProjectID)); err == nil && ok {
		origin = proj.RepoOriginURL
	}
	repo, err := scmRepoForClaim(s.scm, origin, pr.URL)
	if err != nil {
		return err
	}

	// Validate BEFORE writing. A provider will usually reject a bad branch
	// anyway, but relying on that would mean interpreting an error after the
	// fact rather than refusing an action we already know is wrong.
	exists, err := retargeter.BranchExists(ctx, repo, target)
	if err != nil {
		return mapRetargetErr(err)
	}
	if !exists {
		return fmt.Errorf("%w: %w", ErrTargetBranchNotFound,
			apierr.Invalid("TARGET_BRANCH_NOT_FOUND",
				fmt.Sprintf("Branch %q does not exist on the remote", target), nil))
	}

	ref := ports.SCMPRRef{Repo: repo, Number: pr.Number, URL: pr.URL}
	if err := retargeter.RetargetPR(ctx, ref, target); err != nil {
		return mapRetargetErr(err)
	}
	return nil
}

// scmRetargeter mirrors scmobserve.PRRetargeter. The service type-asserts the
// provider against it rather than widening scmProvider, matching how optional
// write capabilities are handled elsewhere — a read-only provider stays valid.
type scmRetargeter interface {
	BranchExists(ctx context.Context, repo ports.SCMRepo, branch string) (bool, error)
	RetargetPR(ctx context.Context, ref ports.SCMPRRef, target string) error
}

// mapRetargetErr converts provider-neutral SCM sentinels into API errors that
// state the CAUSE. Only a genuinely unclassifiable failure becomes "the SCM is
// unavailable" — every case we can name, we name.
func mapRetargetErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ports.ErrSCMInvalid):
		return fmt.Errorf("%w: %w", ErrRetargetRefused,
			apierr.Invalid("RETARGET_REFUSED",
				"The forge refused this retarget. The pull request may already be merged or closed, "+
					"or the target may be the same branch the work is on.", nil))
	case errors.Is(err, ports.ErrSCMForbidden):
		// Wraps the existing sentinel so the controller's 403 mapping still
		// applies, while carrying a cause the human can act on.
		return fmt.Errorf("%w: no permission to retarget this pull request — "+
			"the token may lack write access, or the target branch may be protected", ErrSCMWriteForbidden)
	case errors.Is(err, ports.ErrSCMNotFound):
		return apierr.NotFound("PR_NOT_FOUND", "The pull request was not found on the forge")
	default:
		return fmt.Errorf("%w: %w", ErrSCMUnavailable, err)
	}
}
