package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrSCMPRNotFound is the legacy PR-observation not-found sentinel. It aliases
// the provider-neutral SCM sentinel so old PRObservation callers and new SCM
// callers compose under errors.Is.
var ErrSCMPRNotFound = ErrSCMNotFound

// PRObserver fetches one legacy PR observation by canonical PR URL.
type PRObserver interface {
	Observe(ctx context.Context, prURL string) (PRObservation, error)
}

// PRObservation is what the SCM poller reports for one PR. Fetched is the
// failed-fetch guard: when false the rest is meaningless and lifecycle must not
// read it as "PR closed". Checks/Comments are observation DTOs, not persistence
// rows; the PR Manager owns mapping them into stored domain.PullRequest rows.
type PRObservation struct {
	Fetched      bool
	URL          string
	Number       int
	Title        string
	SourceBranch string
	TargetBranch string
	// HeadSHA is the PR head commit SHA when the provider reports one. Lifecycle
	// anchors the merge-conflict nudge signature on it so a conflict that SURVIVES
	// the worker's push is announced again instead of being deduped away as the
	// episode already reported.
	HeadSHA      string
	Draft        bool
	Merged       bool
	Closed       bool
	CI           domain.CIState
	Review       domain.ReviewDecision
	Mergeability domain.Mergeability
	// MergeabilityStale is true when Mergeability was preserved from the local DB
	// row rather than freshly fetched from the provider this cycle (a review-only
	// refresh, or a metadata fetch that failed). Lifecycle must not raise a
	// merge-conflict nudge from a stale mergeability value: the stored conflict may
	// already be resolved server-side, and nudging a worker to rebase an
	// already-clean branch drags it into needless, potentially destructive work.
	MergeabilityStale bool
	Checks            []PRCheckObservation
	Comments          []PRCommentObservation
}

// PRCheckObservation is one SCM check result on the observed PR.
type PRCheckObservation struct {
	Name       string
	CommitHash string
	Status     domain.PRCheckStatus
	URL        string
	LogTail    string
}

// PRCommentObservation is one review comment observed on the PR.
type PRCommentObservation struct {
	ID     string
	Author string
	// ThreadID is the provider's review-thread identifier this comment belongs to.
	// Several comments share it: a thread is one conversation the human sees as a
	// single open item on the forge, so consumers that count or list review
	// feedback must group by it rather than treating each comment as its own item.
	// Empty for observations that predate review threads, which carry one comment
	// per thread.
	ThreadID string
	File     string
	Line     int
	Body     string
	Resolved bool
	// SelfReply is true when the comment is OUR side replying on a thread that
	// already existed — authored by the PR author (in AO's worker model, the
	// identity the worker replies with) and not the note that opened the thread. It
	// is our own reply, not feedback to address: it must never be presented back to
	// the worker and must never count as review work. The note that OPENS a thread
	// is never a self reply, even under our identity, because the human can review
	// their own worker's PR from the very account that opened it.
	SelfReply bool
	// System is true when the provider marks the note as auto-generated activity
	// (e.g. GitLab's "changed this line in version N of the diff") rather than
	// human-authored feedback.
	System bool
}
