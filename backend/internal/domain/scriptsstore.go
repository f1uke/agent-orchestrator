package domain

import "time"

// ScriptsStoreState is where a workspace's store worktree is in its life.
type ScriptsStoreState string

// Store worktree states.
const (
	// ScriptsStoreActive is a worktree a running (or restorable) workspace
	// writes scripts into.
	ScriptsStoreActive ScriptsStoreState = "active"
	// ScriptsStoreHeld is a worktree teardown kept because removing it would
	// lose work: files never committed, or commits the store's main checkout
	// refused. HeldReason says which.
	ScriptsStoreHeld ScriptsStoreState = "held"
	// ScriptsStoreRemoved is a worktree whose work reached the store and whose
	// folder and branch are gone.
	ScriptsStoreRemoved ScriptsStoreState = "removed"
)

// Live reports whether the worktree still exists for its workspace.
func (s ScriptsStoreState) Live() bool {
	return s == ScriptsStoreActive || s == ScriptsStoreHeld
}

// ScriptsStoreHold names why a store worktree's work did not reach the store's
// main checkout. It is both a publish refusal and, once teardown keeps the
// worktree over it, the row's HeldReason.
type ScriptsStoreHold string

// Reasons a publish is refused or a teardown keeps the worktree.
const (
	// HoldUncommitted means the worktree holds files nobody committed. Only
	// commits publish, so these would be lost with the folder.
	HoldUncommitted ScriptsStoreHold = "uncommitted"
	// HoldPublishConflict means the branch conflicts with the store's base
	// branch. The agent merges the base into its branch, resolves, commits and
	// publishes again.
	HoldPublishConflict ScriptsStoreHold = "publish_conflict"
	// HoldStoreDirtyOverlap means the store's main checkout has uncommitted
	// edits in files the merge would change.
	HoldStoreDirtyOverlap ScriptsStoreHold = "store_dirty_overlap"
	// HoldStoreOffBase means the store's main checkout is not on the branch the
	// worktree was cut from, so there is nothing safe to merge into.
	HoldStoreOffBase ScriptsStoreHold = "store_off_base"
	// HoldPublishFailed is any other git failure (an index lock, an operation
	// paused in the main checkout). The publish is retried as it is.
	HoldPublishFailed ScriptsStoreHold = "publish_failed"
)

// ScriptsStoreWorktree is one workspace's own git worktree of the project's
// mobile scripts store. Every task writes its scripts here and publishes them
// into the store's main checkout, instead of every session editing that one
// shared checkout.
//
// The row is per WORKSPACE, not per session: a crew's members share dev's repo
// worktree and so share dev's store worktree, keyed by the crew id.
type ScriptsStoreWorktree struct {
	// SessionID is the workspace owner: a solo worker itself, or dev (the crew
	// id) for a crew.
	SessionID SessionID
	ProjectID ProjectID
	// Store is the absolute path of the store's main checkout.
	Store string
	// Path is <DataDir>/store-worktrees/<base(Store)>/<SessionID>.
	Path string
	// Branch is ao/<SessionID>.
	Branch string
	// BaseBranch is the branch the store had checked out when the worktree was
	// cut; publishing merges into it.
	BaseBranch string
	State      ScriptsStoreState
	// HeldReason is empty unless State is held.
	HeldReason ScriptsStoreHold
	// HeldFiles are the files the hold names: the uncommitted files, the
	// conflicting files, or the main checkout's files in the way.
	HeldFiles []string
	// Uncommitted and Unpublished are the worktree's git facts as last read:
	// files nobody committed, and commits the base branch does not have. The
	// lifecycle refresh writes them only when they change, so the board learns
	// of a change through the row's own CDC event.
	Uncommitted []string
	Unpublished int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// PublishedAt is the last publish that moved the base branch; zero if none.
	PublishedAt time.Time
}
