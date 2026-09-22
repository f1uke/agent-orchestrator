import { describe, expect, it } from "vitest";
import { type ChangesScope, changesEmptyState, changesScopeNotice } from "./changes-scope";

const onBranch: ChangesScope & { headLabel?: string } = {
	targetBranch: "develop",
	branch: "feature/x",
	branchMissing: false,
	diffSubject: "branch",
	includesWorktree: true,
	pendingPaths: 0,
	headState: "on_branch",
};

describe("changesScopeNotice", () => {
	it("says nothing when the branch and the worktree agree", () => {
		expect(changesScopeNotice(onBranch)).toBeNull();
	});

	// The defect: 38 committed files described as "nothing to review". The note
	// has to name BOTH the thing that was measured and where the worktree stands.
	it("names the detached worktree and the branch that was listed instead", () => {
		const notice = changesScopeNotice({
			...onBranch,
			includesWorktree: false,
			headState: "detached",
			headLabel: "a058308",
			pendingPaths: 3,
		});
		expect(notice?.headline).toBe("Worktree detached at a058308");
		expect(notice?.detail).toContain("branch feature/x's commits");
		expect(notice?.detail).toContain("develop");
		// The work it left out is declared, not dropped.
		expect(notice?.detail).toContain("3 uncommitted paths");
	});

	it("pluralises a single left-out path", () => {
		const notice = changesScopeNotice({
			...onBranch,
			includesWorktree: false,
			headState: "detached",
			headLabel: "a058308",
			pendingPaths: 1,
		});
		expect(notice?.detail).toContain("1 uncommitted path is measured");
	});

	it("leaves the pending clause out when there is none, rather than saying nothing is there", () => {
		const notice = changesScopeNotice({
			...onBranch,
			includesWorktree: false,
			headState: "detached",
			headLabel: "a058308",
			pendingPaths: 0,
		});
		expect(notice?.detail).not.toContain("uncommitted");
	});

	it("names another checked-out branch", () => {
		const notice = changesScopeNotice({
			...onBranch,
			includesWorktree: false,
			headState: "other_branch",
			headLabel: "spike/try",
		});
		expect(notice?.headline).toBe("Worktree is on spike/try");
	});

	// A branch ref that is not here is a THIRD state: not "nothing changed", and
	// not "the worktree is parked" either.
	it("says when the session's branch could not be found at all", () => {
		const notice = changesScopeNotice({
			...onBranch,
			diffSubject: "head",
			branchMissing: true,
			headState: "detached",
			headLabel: "a058308",
		});
		expect(notice?.headline).toBe("Branch feature/x is not in this worktree");
		expect(notice?.detail).toContain("HEAD");
	});

	it("says when a detached worktree is all there is to measure", () => {
		const notice = changesScopeNotice({
			...onBranch,
			branch: "",
			diffSubject: "head",
			headState: "detached",
			headLabel: "a058308",
		});
		expect(notice?.headline).toBe("Worktree detached at a058308");
		expect(notice?.detail).toContain("records no branch");
	});
});

describe("changesEmptyState", () => {
	it("claims the branch matches its target only when that is what was measured", () => {
		const empty = changesEmptyState(onBranch);
		expect(empty.title).toBe("No changes vs develop");
		expect(empty.detail).toContain("Branch feature/x matches its target branch");
	});

	// The exact false claim the human read. An empty list from a detached worktree
	// must never be phrased as "this branch matches its target branch".
	it("never claims a match when the worktree is parked elsewhere", () => {
		const empty = changesEmptyState({
			...onBranch,
			includesWorktree: false,
			headState: "detached",
			headLabel: "a058308",
			pendingPaths: 2,
		});
		expect(empty.detail).not.toContain("matches its target branch");
		expect(empty.title).toBe("No commits vs develop");
		expect(empty.detail).toContain("detached at a058308");
		expect(empty.detail).toContain("2 uncommitted paths");
	});

	it("says the branch was never found rather than that it is empty", () => {
		const empty = changesEmptyState({
			...onBranch,
			diffSubject: "head",
			branchMissing: true,
			headState: "on_branch",
		});
		expect(empty.title).toBe("Branch feature/x is not in this worktree");
		expect(empty.detail).toContain("Nothing was measured from that branch");
	});
});
