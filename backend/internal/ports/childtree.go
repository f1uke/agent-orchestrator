package ports

import "context"

// ChildTrees is the git side of a worker's child worktrees: each one is cut
// from the worker's HEAD into a folder outside the worker's own, and its
// commits are merged back into the worker's branch in the worker's worktree.
//
// Every operation is non-destructive unless its name says otherwise: nothing
// here deletes a commit, and only Preserve removes a dirty folder, after it has
// committed what the folder held.
type ChildTrees interface {
	// Create adds a worktree at spec.Path on a new branch spec.Branch cut from
	// the worker's HEAD. It is idempotent: a path already registered on that
	// branch is returned as it is.
	Create(ctx context.Context, spec ChildTreeSpec) (ChildTreeCreated, error)
	// Inspect reports what a child's worktree holds that targetBranch does
	// not: its commits and files are counted from where it meets targetBranch,
	// so a child that rebased onto the worker's branch is not credited with the
	// worker's own commits. baseSHA is the fallback when targetBranch is gone.
	Inspect(ctx context.Context, path, baseSHA, targetBranch string) (ChildTreeFacts, error)
	// CommitAll stages everything in the child's worktree and commits it with
	// the repository's hooks running.
	CommitAll(ctx context.Context, path, message string) error
	// Conflicts names the files a merge of branch into targetBranch would
	// conflict on, computed without touching any worktree.
	Conflicts(ctx context.Context, workerPath, targetBranch, branch string) ([]string, error)
	// Head returns the commit a worktree has checked out.
	Head(ctx context.Context, path string) (string, error)
	// Merge merges branch into the worker's worktree with a merge commit. A
	// merge that cannot run now (the worker is on another branch, has a git
	// operation paused, or has uncommitted edits in the files the merge
	// touches) is reported as Held with a reason and leaves the worker's tree
	// exactly as it was.
	Merge(ctx context.Context, workerPath, targetBranch, branch, message string) (ChildMergeResult, error)
	// RecoverMerge settles a merge a crash may have interrupted. headBefore is
	// the worker HEAD recorded before the merge started.
	RecoverMerge(ctx context.Context, workerPath, branch, headBefore string) (ChildMergeResult, error)
	// Remove deletes a clean child worktree, and its branch when deleteBranch
	// is set and the branch is merged into the worker's HEAD.
	Remove(ctx context.Context, workerPath, path, branch string, deleteBranch bool) error
	// Preserve commits whatever the child's folder holds onto its branch
	// without running hooks, then removes the folder and keeps the branch.
	// It reports whether anything had to be committed.
	Preserve(ctx context.Context, workerPath, path, branch, message string) (bool, error)
}

// ChildTreeSpec says where a child worktree goes.
type ChildTreeSpec struct {
	// WorkerPath is the worker's worktree, whose HEAD the child is cut from.
	WorkerPath string
	Path       string
	Branch     string
}

// ChildTreeCreated is what Create observed in the worker when it cut the child.
type ChildTreeCreated struct {
	BaseSHA      string
	TargetBranch string
	// WorkerDirty lists the worker's uncommitted paths, which the child does not
	// get.
	WorkerDirty []string
}

// ChildTreeFacts is the state of a child's worktree.
type ChildTreeFacts struct {
	Dirty        bool
	Commits      int
	FilesChanged int
	Head         string
}

// ChildMergeResult is how a merge (or a merge recovery) ended.
type ChildMergeResult struct {
	Merged bool
	SHA    string
	// Held is why the merge did not run, empty when it did.
	Held string
}
