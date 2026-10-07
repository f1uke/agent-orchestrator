package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ScriptsTrees is the git work behind a workspace's own worktree of the mobile
// scripts store: cutting it from the store's main checkout, reading what it
// holds, publishing its commits back, and removing it.
type ScriptsTrees interface {
	// Probe reads whether a store can have worktrees cut from it, and from
	// which branch. A store that cannot (missing, not a git repository,
	// detached, no commits) is OK=false with the reason, not an error.
	Probe(ctx context.Context, store string) (ScriptsStoreProbe, error)
	// Ensure converges a worktree at path on branch: a registered folder is
	// repaired, a missing one is added back from branch, and branch is cut
	// from base when it does not exist yet. Running it again changes nothing.
	Ensure(ctx context.Context, store, path, branch, base string) error
	// Status reads a worktree's uncommitted files and the commits on branch
	// that base does not have.
	Status(ctx context.Context, path, branch, base string) (ScriptsTreeStatus, error)
	// Dirty lists the uncommitted files of the store's main checkout itself.
	Dirty(ctx context.Context, store string) ([]string, error)
	// Publish merges branch into base in the store's main checkout: a fast
	// forward when it can, a merge commit with message otherwise. A merge that
	// cannot run is Refused with the hold and the files, and leaves the main
	// checkout exactly as it was.
	Publish(ctx context.Context, store, branch, base, message string) (ScriptsPublishResult, error)
	// Remove deletes the worktree and its branch. Without force, git refuses a
	// worktree with uncommitted files and a branch base does not contain.
	Remove(ctx context.Context, store, path, branch string, force bool) error
	// Registered lists the worktree folders the store's git knows about,
	// including ones whose folder is gone.
	Registered(ctx context.Context, store string) ([]string, error)
}

// ScriptsStoreProbe is what a store's main checkout says about itself.
type ScriptsStoreProbe struct {
	// Base is the branch checked out in the store.
	Base string
	OK   bool
	// Reason says why OK is false.
	Reason string
}

// ScriptsTreeStatus is what a store worktree holds that the store does not.
type ScriptsTreeStatus struct {
	Uncommitted []string
	Unpublished int
}

// ScriptsPublishOutcome is what a publish did.
type ScriptsPublishOutcome string

// Publish outcomes.
const (
	// PublishNothing means the branch had no commits the base lacks.
	PublishNothing ScriptsPublishOutcome = "nothing"
	// PublishFastForward means the base moved to the branch's tip.
	PublishFastForward ScriptsPublishOutcome = "fast_forward"
	// PublishMerged means AO committed a merge of the branch onto the base.
	PublishMerged ScriptsPublishOutcome = "merged"
	// PublishRefused means the merge could not run; Hold says why.
	PublishRefused ScriptsPublishOutcome = "refused"
)

// ScriptsPublishResult reports one publish.
type ScriptsPublishResult struct {
	Outcome ScriptsPublishOutcome
	// SHA is the base's new tip after a fast forward or merge.
	SHA string
	// Commits is how many commits the branch had that the base lacked.
	Commits int
	// Hold, Detail and Files explain a refusal.
	Hold   domain.ScriptsStoreHold
	Detail string
	Files  []string
}
