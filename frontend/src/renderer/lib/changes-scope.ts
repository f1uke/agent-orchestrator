import type { WorkspaceChanges } from "../hooks/useWorkspaceChanges";

/**
 * The scope facts on a Changes payload: WHAT was compared, and where the
 * worktree was standing while it was.
 *
 * These exist because the panel once answered a question nobody asked. A worker
 * had committed 38 files on its branch and then checked out its base commit - a
 * legitimate thing to do, to install a baseline build and prove an upgrade path
 * against it - and the panel, which measured the worktree's HEAD, reported "No
 * changes vs develop / This branch matches its target branch". That reads as
 * "this session has done nothing", which was the opposite of the truth.
 *
 * So the daemon now diffs the branch the session OWNS and reports its own
 * position, and every sentence this module writes names what was measured.
 */
export type ChangesScope = Pick<
	WorkspaceChanges,
	"targetBranch" | "branch" | "branchMissing" | "diffSubject" | "includesWorktree" | "pendingPaths" | "headState"
>;

type Scope = ChangesScope & { headLabel?: string };

/** The work left out of the list, with the verb that agrees with it. */
function pending(count: number | undefined): { text: string; verb: string } | null {
	const n = count ?? 0;
	if (n === 0) return null;
	return { text: `${n} uncommitted ${n === 1 ? "path" : "paths"}`, verb: n === 1 ? "is" : "are" };
}

/** Where the worktree is, as a phrase: "detached at a058308" / "on spike/try". */
function where(scope: Scope): string {
	if (scope.headState === "detached") return `detached at ${scope.headLabel || "an unnamed commit"}`;
	if (scope.headState === "other_branch") return `on ${scope.headLabel || "another branch"}`;
	return "on this session's branch";
}

/** The same place, referred to a second time: "that checkout" / "that branch". */
function thatStandpoint(scope: Scope): string {
	return scope.headState === "other_branch" ? "that branch" : "that checkout";
}

/**
 * The notice to show above the list, or null when the payload answers exactly
 * the question the reader assumes: this session's branch, worktree included.
 *
 * A detached worktree is NOT an error and is never presented as one - it is a
 * normal thing for a session to do. It is presented as what it is: a reason the
 * numbers on screen mean something slightly different.
 */
export function changesScopeNotice(scope: Scope): { headline: string; detail: string } | null {
	const target = scope.targetBranch || "the target branch";
	if (scope.branchMissing) {
		return {
			headline: `Branch ${scope.branch} is not in this worktree`,
			detail: `It may have been renamed or deleted. Showing the worktree's HEAD against ${target} instead, which is ${where(
				scope,
			)}.`,
		};
	}
	if (scope.headState === "detached" && !scope.branch) {
		return {
			headline: `Worktree detached at ${scope.headLabel || "a commit"}`,
			detail: `This session records no branch of its own, so this compares that checkout against ${target}.`,
		};
	}
	if (scope.headState === "detached" || scope.headState === "other_branch") {
		const left = pending(scope.pendingPaths);
		return {
			headline:
				scope.headState === "detached"
					? `Worktree detached at ${scope.headLabel}`
					: `Worktree is on ${scope.headLabel}`,
			detail: `Listing branch ${scope.branch}'s commits against ${target}.${
				left
					? ` The worktree's ${left.text} ${left.verb} measured against ${thatStandpoint(scope)}, so ${
							left.verb === "is" ? "it is" : "they are"
						} not listed.`
					: ""
			}`,
		};
	}
	return null;
}

/**
 * The empty-list state. "No changes / this branch matches its target branch" is
 * a positive claim, so it is only made when the branch is what was measured and
 * the worktree was standing on it. Every other shape says which thing came back
 * empty and what was left out of the question.
 */
export function changesEmptyState(scope: Scope): { title: string; detail: string } {
	const target = scope.targetBranch || "target";
	const branch = scope.branch;
	if (scope.branchMissing) {
		return {
			title: `Branch ${branch} is not in this worktree`,
			detail: `Nothing was measured from that branch. The worktree's HEAD, which is ${where(
				scope,
			)}, has no changes vs ${target}.`,
		};
	}
	if (scope.headState === "detached" && !branch) {
		return {
			title: `No changes vs ${target}`,
			detail: `The worktree is ${where(scope)} and matches ${target}. This session records no branch of its own, so nothing else was measured.`,
		};
	}
	if (scope.headState === "detached" || scope.headState === "other_branch") {
		const left = pending(scope.pendingPaths);
		return {
			title: `No commits vs ${target}`,
			detail: `Branch ${branch} has committed nothing beyond ${target}. The worktree is ${where(scope)}${
				left
					? `, and its ${left.text} ${left.verb} measured against ${thatStandpoint(scope)} rather than the branch`
					: ""
			}.`,
		};
	}
	return {
		title: `No changes vs ${target}`,
		detail: branch
			? `Branch ${branch} matches its target branch. Nothing to review yet.`
			: "This branch matches its target branch. Nothing to review yet.",
	};
}
