package domain

import (
	"regexp"
	"time"
)

// A child is one of a worker's subagents that Claude Code launched with
// `isolation: "worktree"`. AO creates its worktree (Claude Code's WorktreeCreate
// hook hands creation over entirely), records it here, merges its commits back
// into the worker's branch when it stops, and removes it. A child is not a
// session: it has no process, pane or pull request of its own. It lives inside
// the worker's Claude Code process and dies with it.

// ChildState is where a child's work is relative to the worker's branch.
type ChildState string

// Child states.
const (
	// ChildRunning is a child whose subagent is (or was last seen) working.
	ChildRunning ChildState = "running"
	// ChildMerging is the window in which AO runs `git merge` in the worker's
	// worktree. A row left here by a crash is reconciled at daemon start.
	ChildMerging ChildState = "merging"
	// ChildHeld means the merge would be clean but could not run now: the
	// worker has uncommitted edits in the same files, a git operation is paused
	// in its tree, or it is on another branch. AO retries on the worker's next
	// hook.
	ChildHeld ChildState = "held"
	// ChildConflict means the child's commits conflict with the worker's branch
	// and the child did not resolve it. The branch and folder are kept for the
	// worker to merge by hand.
	ChildConflict ChildState = "conflict"
	// ChildMerged means the child's commits are on the worker's branch and its
	// folder and branch are gone.
	ChildMerged ChildState = "merged"
	// ChildRemoved means the child finished with nothing to merge.
	ChildRemoved ChildState = "removed"
	// ChildPreserved means the worker was torn down before the child's work
	// reached its branch. The work is committed on the child's branch, which is
	// kept; only the folder is gone.
	ChildPreserved ChildState = "preserved"
)

// Valid reports whether s is a known state.
func (s ChildState) Valid() bool {
	switch s {
	case ChildRunning, ChildMerging, ChildHeld, ChildConflict, ChildMerged, ChildRemoved, ChildPreserved:
		return true
	}
	return false
}

// Undelivered reports whether the child may hold work that is not yet on the
// worker's branch and not yet set aside on a kept branch. A teardown that would
// end the worker while any child is undelivered is refused.
func (s ChildState) Undelivered() bool {
	switch s {
	case ChildRunning, ChildMerging, ChildHeld, ChildConflict:
		return true
	}
	return false
}

// SessionChild is one child worktree of a worker session.
type SessionChild struct {
	SessionID SessionID `json:"sessionId"`
	ProjectID ProjectID `json:"projectId"`
	// AgentID is Claude Code's id for the subagent (WorktreeCreate's name
	// without the "agent-" prefix).
	AgentID string `json:"agentId"`
	// ParentAgentID is empty for a direct child of the worker. It is reserved
	// for nesting, which v1 refuses.
	ParentAgentID string `json:"parentAgentId,omitempty"`
	AgentType     string `json:"agentType,omitempty"`
	Description   string `json:"description,omitempty"`
	Branch        string `json:"branch"`
	// TargetBranch is the worker's branch when the child was cut. AO merges
	// into it only while the worker still has it checked out.
	TargetBranch string `json:"targetBranch"`
	BaseSHA      string `json:"baseSha"`
	// BaseDirty lists worker files that were uncommitted when the child was cut,
	// which the child therefore cannot see.
	BaseDirty    []string   `json:"baseDirty,omitempty"`
	WorktreePath string     `json:"worktreePath"`
	State        ChildState `json:"state"`
	// MergeHeadBefore is the worker's HEAD just before AO started a merge. It
	// tells crash recovery whether a half-done merge is AO's to abort.
	MergeHeadBefore string `json:"-"`
	MergedSHA       string `json:"mergedSha,omitempty"`
	Commits         int    `json:"commits"`
	FilesChanged    int    `json:"filesChanged"`
	// Detail is the human-readable reason behind held, conflict and preserved.
	Detail     string `json:"detail,omitempty"`
	StopBlocks int    `json:"-"`
	// NotifiedState is the last state the worker was told about.
	NotifiedState ChildState `json:"-"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	FinishedAt    *time.Time `json:"finishedAt,omitempty"`
}

// childAgentName is the name Claude Code gives WorktreeCreate for a subagent.
// EnterWorktree and `--worktree` pass a user-chosen name instead, which AO
// refuses: those move the worker itself, not a child.
var childAgentName = regexp.MustCompile(`^agent-([a-z0-9]+)$`)

// ParseChildAgentName extracts the subagent id from a WorktreeCreate name.
func ParseChildAgentName(name string) (string, bool) {
	m := childAgentName.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// ChildStopBlockLimit is how many times AO refuses a child's stop to make it
// commit or rebase before AO stops asking and acts itself. Asking first keeps
// the child's own commit messages and conflict resolutions; the limit keeps a
// child that cannot comply from looping.
const ChildStopBlockLimit = 2

// ChildStopFacts is what AO observed in a child's worktree when it stopped.
type ChildStopFacts struct {
	Dirty bool
	// Commits counts commits on the child's branch beyond its base.
	Commits int
	// Conflicts names files that would conflict when merging into the worker's
	// branch. Only meaningful when the tree is clean and Commits > 0.
	Conflicts []string
	// Blocks is how many stops AO has already refused.
	Blocks int
}

// ChildStopAction is what AO does about a stopping child.
type ChildStopAction string

// Child stop actions.
const (
	ChildStopBlockCommit ChildStopAction = "block_commit"
	ChildStopAutoCommit  ChildStopAction = "auto_commit"
	ChildStopRemove      ChildStopAction = "remove"
	ChildStopBlockRebase ChildStopAction = "block_rebase"
	ChildStopPark        ChildStopAction = "park_conflict"
	ChildStopMerge       ChildStopAction = "merge"
)

// DecideChildStop picks the next step for a stopping child. mayBlock is false
// when there is no live subagent to ask (its worker's process restarted), in
// which case AO acts without asking. After ChildStopAutoCommit the caller
// re-inspects the tree and decides again.
func DecideChildStop(f ChildStopFacts, mayBlock bool) ChildStopAction {
	ask := mayBlock && f.Blocks < ChildStopBlockLimit
	switch {
	case f.Dirty && ask:
		return ChildStopBlockCommit
	case f.Dirty:
		return ChildStopAutoCommit
	case f.Commits == 0:
		return ChildStopRemove
	case len(f.Conflicts) > 0 && ask:
		return ChildStopBlockRebase
	case len(f.Conflicts) > 0:
		return ChildStopPark
	}
	return ChildStopMerge
}
