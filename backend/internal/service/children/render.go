package children

import (
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// renderBrief is the child's standing context. It is written to the subagent,
// which sees nothing of the worker's system prompt.
func renderBrief(child domain.SessionChild, ios bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## AO child worktree\n\n")
	fmt.Fprintf(&b, "You run in your own git worktree that AO (Agent Orchestrator) created for you: folder %s, branch %s, cut from %s at %s.\n\n",
		child.WorktreePath, child.Branch, child.TargetBranch, shortSHA(child.BaseSHA))
	fmt.Fprintf(&b, "- Work only inside this folder. Do not push, do not switch or create branches, and do not touch any other worktree.\n")
	fmt.Fprintf(&b, "- Commit your work on this branch before you finish: `git add -A && git commit -m \"<what you did>\"`. When you stop, AO merges your commits into %s. If they conflict, AO asks you to rebase onto %s and resolve the conflicts.\n",
		child.TargetBranch, child.TargetBranch)
	if len(child.BaseDirty) > 0 {
		fmt.Fprintf(&b, "- The worker had uncommitted changes in %s when you were created. You do not have them.\n", strings.Join(child.BaseDirty, ", "))
	}
	if ios {
		fmt.Fprintf(&b, "- Build with your own DerivedData, never the worker's: add `-derivedDataPath %s` to every xcodebuild. AO deletes it when your work is merged.\n", DerivedDataPath(child))
		fmt.Fprintf(&b, "- Leave simulator runs (`xcodebuild test`, Maestro flows) to the worker, which runs them after your work is merged.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderNote tells the worker what happened to one child since it last heard.
func renderNote(child domain.SessionChild) string {
	label := childLabel(child)
	switch child.State {
	case domain.ChildMerged:
		note := fmt.Sprintf("AO merged subagent %s into %s: %d %s, %d %s changed (merge %s). Its worktree is removed.",
			label, child.TargetBranch, child.Commits, plural(child.Commits, "commit"), child.FilesChanged, plural(child.FilesChanged, "file"), shortSHA(child.MergedSHA))
		if child.Detail != "" {
			note += " Note: " + child.Detail + "."
		}
		return note
	case domain.ChildRemoved:
		return fmt.Sprintf("Subagent %s finished without commits, so there was nothing to merge. AO removed its worktree.", label)
	case domain.ChildHeld:
		return fmt.Sprintf("AO could not merge subagent %s into %s yet: %s. Its branch %s is kept, and AO retries after your next tool call. Commit or stash the files in the way to unblock it.",
			label, child.TargetBranch, child.Detail, child.Branch)
	case domain.ChildConflict:
		return fmt.Sprintf("Subagent %s's commits %s, and the subagent did not resolve it. Its branch %s and worktree %s are kept. Merge it yourself: `git merge %s`, resolve, commit.",
			label, child.Detail, child.Branch, child.WorktreePath, child.Branch)
	case domain.ChildPreserved:
		return fmt.Sprintf("Subagent %s's work was %s.", label, child.Detail)
	}
	return fmt.Sprintf("Subagent %s is %s.", label, child.State)
}

func childLabel(child domain.SessionChild) string {
	switch {
	case child.Description != "":
		return fmt.Sprintf("%q (%s)", child.Description, child.AgentID)
	case child.AgentType != "":
		return child.AgentType + " (" + child.AgentID + ")"
	}
	return child.AgentID
}

func shortSHA(sha string) string {
	if len(sha) > 9 {
		return sha[:9]
	}
	return sha
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
