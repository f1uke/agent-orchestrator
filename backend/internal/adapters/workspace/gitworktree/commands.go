package gitworktree

import "strings"

func checkRefFormatBranchArgs(repo, branch string) []string {
	return []string{"-C", repo, "check-ref-format", "--branch", branch}
}

func revParseVerifyArgs(repo, ref string) []string {
	return []string{"-C", repo, "rev-parse", "--verify", "--quiet", ref}
}

func worktreeAddBranchArgs(repo, path, branch string) []string {
	return []string{"-C", repo, "worktree", "add", path, branch}
}

func worktreeAddNewBranchArgs(repo, branch, path, baseRef string) []string {
	return []string{"-C", repo, "worktree", "add", "-b", branch, path, baseRef}
}

// worktreeRemoveArgs intentionally omits --force: a dirty worktree (uncommitted
// agent work) MUST cause `git worktree remove` to fail, so the post-prune
// "still registered" check in Destroy surfaces the refusal to the Session
// Manager's Cleanup, which routes the session to Skipped rather than deleting
// the agent's in-progress changes.
func worktreeRemoveArgs(repo, path string) []string {
	return []string{"-C", repo, "worktree", "remove", path}
}

// worktreeForceRemoveArgs passes --force to bypass git's dirty-worktree check.
// Only ForceDestroy may call this. It is safe only AFTER the session's
// uncommitted work has been captured (Task 2's StashUncommitted). Callers that
// have not yet captured work must use worktreeRemoveArgs / Destroy instead.
func worktreeForceRemoveArgs(repo, path string) []string {
	return []string{"-C", repo, "worktree", "remove", "--force", path}
}

func worktreePruneArgs(repo string) []string {
	return []string{"-C", repo, "worktree", "prune"}
}

// worktreeUnlockArgs clears a `git worktree lock` so removal/prune can proceed.
// A locked worktree makes `git worktree remove` refuse ("cannot remove a locked
// working tree") and `git worktree prune` skip it, so AO teardown must unlock a
// worktree it owns before tearing it down. External tooling occasionally leaves
// such a lock on a managed worktree (e.g. the canonical orchestrator worktree),
// which otherwise wedges every restart.
func worktreeUnlockArgs(repo, path string) []string {
	return []string{"-C", repo, "worktree", "unlock", path}
}

// statusPorcelainArgs probes the worktree at path for uncommitted changes or
// untracked files — the condition `git worktree remove` (without --force)
// refuses on — so Destroy can classify a refusal as ports.ErrWorkspaceDirty.
func statusPorcelainArgs(path string) []string {
	return []string{"-C", path, "status", "--porcelain"}
}

func worktreeListPorcelainArgs(repo string) []string {
	return []string{"-C", repo, "worktree", "list", "--porcelain"}
}

// addAllTempIndexArgs stages all tracked and non-ignored untracked files into a
// temp index file without touching the real index or the working tree.
// GIT_INDEX_FILE must be set in the command's environment before calling.
func addAllTempIndexArgs(worktree string) []string {
	return []string{"-C", worktree, "add", "-A"}
}

// writeTreeArgs flushes the temp index into a tree object and prints the SHA.
// GIT_INDEX_FILE must be set in the command's environment.
func writeTreeArgs(worktree string) []string {
	return []string{"-C", worktree, "write-tree"}
}

// commitTreeArgs creates a commit object from a tree SHA. parent is the HEAD
// SHA to set as parent; message is the commit message. When parent is empty
// (unborn HEAD), the -p flag is omitted.
func commitTreeArgs(worktree, treeSHA, parent, message string) []string {
	args := []string{"-C", worktree, "commit-tree", treeSHA}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	args = append(args, "-m", message)
	return args
}

// updateRefArgs creates or moves a ref to point at a commit SHA.
func updateRefArgs(worktree, ref, commitSHA string) []string {
	return []string{"-C", worktree, "update-ref", ref, commitSHA}
}

// deleteRefArgs deletes a ref unconditionally.
func deleteRefArgs(worktree, ref string) []string {
	return []string{"-C", worktree, "update-ref", "-d", ref}
}

// revParseHeadArgs returns the HEAD commit SHA in the worktree.
// Exit code 128 means the repo has no commits (unborn HEAD).
func revParseHeadArgs(worktree string) []string {
	return []string{"-C", worktree, "rev-parse", "--verify", "HEAD"}
}

// cherryPickNoCommitArgs applies a single commit's diff onto the current
// working tree via a true three-way merge without committing or moving HEAD.
// git cherry-pick --no-commit computes the diff between <sha> and its parent
// and 3-way-merges it onto the current working tree. On conflict it leaves
// textual conflict markers in the affected files and exits non-zero. New files
// added in the preserve commit come through as additions. Because -n is used,
// no sequencer state is left that would require a cherry-pick --quit afterward.
func cherryPickNoCommitArgs(worktree, commitSHA string) []string {
	return []string{"-C", worktree, "cherry-pick", "--no-commit", commitSHA}
}

// ignoredCountArgs lists files skipped because of .gitignore (dry-run, no mutation).
func ignoredCountArgs(worktree string) []string {
	return []string{"-C", worktree, "status", "--ignored", "--porcelain"}
}

// fetchBaseArgs refreshes origin's view of the base branch in the shared repo.
// The refspec is explicit so the fetch stays narrow and updates the
// remote-tracking ref the sync then fast-forwards onto.
func fetchBaseArgs(repo, baseBranch string) []string {
	return []string{"-C", repo, "fetch", "--quiet", "origin",
		"+refs/heads/" + baseBranch + ":refs/remotes/origin/" + baseBranch}
}

// mergeFFOnlyArgs advances the worktree's branch to ref, or fails. --ff-only is
// load-bearing: it succeeds exactly when the branch has no commits of its own,
// so it can never discard committed work.
func mergeFFOnlyArgs(worktree, ref string) []string {
	return []string{"-C", worktree, "merge", "--ff-only", ref}
}

// syncBaseRefCandidates lists where a base branch may live, remote-tracking
// first so the shared remote wins over a possibly-behind local head. A qualified
// base ("upstream/main") is used verbatim, matching baseRefCandidates.
func syncBaseRefCandidates(baseBranch string) []string {
	if strings.Contains(baseBranch, "/") {
		return []string{baseBranch}
	}
	return []string{"refs/remotes/origin/" + baseBranch, "refs/heads/" + baseBranch}
}

func baseRefCandidates(branch, defaultBranch string) []string {
	candidates := []string{"origin/" + branch}
	if strings.Contains(defaultBranch, "/") {
		// A qualified default ("upstream/main") is used verbatim; git's refname
		// disambiguation already falls back to refs/heads/<defaultBranch>.
		candidates = append(candidates, defaultBranch)
	} else {
		// The local head comes after origin/<defaultBranch> so remote-tracking
		// still wins when present, but a remoteless repo can base new branches
		// on its local default branch instead of failing BRANCH_NOT_FETCHED.
		candidates = append(candidates, "origin/"+defaultBranch, "refs/heads/"+defaultBranch)
	}
	return append(candidates, branch)
}
