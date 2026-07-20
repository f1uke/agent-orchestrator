package scm

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// PRRetargeter is the provider-neutral write-capability contract for changing
// which branch an open pull/merge request merges into.
//
// It sits alongside ReviewThreadWriter rather than widening Provider, following
// the same convention: write capabilities are optional interfaces that callers
// type-assert, so a read-only provider stays valid without implementing them.
//
// AO's outbound SCM writes are deliberately few. This one exists because a
// session's target branch is editable by the human, and an edit that changed
// only AO's copy would leave AO and the forge quietly disagreeing about where
// the work lands. Keeping BranchExists in the same capability is what lets the
// caller refuse a bad target BEFORE attempting the write, rather than
// interpreting a provider rejection after the fact.
type PRRetargeter interface {
	// BranchExists reports whether branch is present on the remote repository.
	// It returns a plain bool rather than leaving the caller to sniff a
	// not-found sentinel, because the two adapters classify 404 differently
	// (GitHub maps it to a typed error, GitLab does not) and that asymmetry
	// must not leak into the retarget decision.
	BranchExists(ctx context.Context, repo ports.SCMRepo, branch string) (bool, error)
	// RetargetPR points the given pull/merge request at target.
	//
	// Implementations must translate a provider refusal (GitHub 422, GitLab
	// 400) into ports.ErrSCMInvalid so the caller can tell "you asked for
	// something impossible" apart from "the service is down". Callers are
	// expected to skip the call entirely when the request is already on target,
	// so implementations need not treat that as a special case.
	RetargetPR(ctx context.Context, ref ports.SCMPRRef, target string) error
}
