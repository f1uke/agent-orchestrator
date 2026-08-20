import { describe, expect, it } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import type { SmokeProgress } from "./smoke-test";
import { deriveReadiness, type ReadinessGateKey, type ReadinessTone } from "./readiness";

const pr = (overrides: Partial<SessionPRSummary> = {}): SessionPRSummary => ({
	url: "https://github.com/acme/repo/pull/7",
	htmlUrl: "https://github.com/acme/repo/pull/7",
	number: 7,
	title: "Fix dashboard",
	state: "open",
	provider: "github",
	repo: "acme/repo",
	author: "ada",
	sourceBranch: "fix/dashboard",
	targetBranch: "main",
	headSha: "abc123",
	additions: 10,
	deletions: 3,
	changedFiles: 2,
	ci: { state: "passing", failingChecks: [] },
	review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
	mergeability: { state: "mergeable", reasons: [], prUrl: "https://github.com/acme/repo/pull/7" },
	updatedAt: "2026-06-15T00:00:00Z",
	observedAt: "2026-06-15T00:00:00Z",
	ciObservedAt: "2026-06-15T00:00:00Z",
	reviewObservedAt: "2026-06-15T00:00:00Z",
	...overrides,
});

const smoke = (overrides: Partial<SmokeProgress> = {}): SmokeProgress => ({
	total: 0,
	pass: 0,
	fail: 0,
	skip: 0,
	pending: 0,
	checked: 0,
	...overrides,
});

const gl = (overrides: Partial<SessionPRSummary> = {}): SessionPRSummary =>
	pr({
		url: "https://gitlab.com/acme/repo/-/merge_requests/3028",
		htmlUrl: "https://gitlab.com/acme/repo/-/merge_requests/3028",
		number: 3028,
		provider: "gitlab",
		mergeability: { state: "mergeable", reasons: [], prUrl: "https://gitlab.com/acme/repo/-/merge_requests/3028" },
		...overrides,
	});

const tones = (r: ReturnType<typeof deriveReadiness>): Record<ReadinessGateKey, ReadinessTone> =>
	Object.fromEntries(r.gates.map((g) => [g.key, g.tone])) as Record<ReadinessGateKey, ReadinessTone>;

const stateOf = (r: ReturnType<typeof deriveReadiness>, key: ReadinessGateKey) =>
	r.gates.find((g) => g.key === key)?.state;

describe("deriveReadiness — verdict", () => {
	it("no PR + active agent → Working (amber, pulsing)", () => {
		const r = deriveReadiness({ activity: { state: "active" }, status: "working" }, [], smoke());
		expect(r.verdict.word).toBe("Working");
		expect(r.verdict.hue).toBe("working");
		expect(r.verdict.pulse).toBe(true);
		expect(r.contextLabel).toBe("");
		expect(tones(r).work).toBe("wait");
		expect(tones(r).pr).toBe("idle");
	});

	it("no PR keeps the headline Working even when a session-scoped smoke check failed", () => {
		// Smoke checks are session-scoped and can exist pre-PR; a failure must not
		// headline over "Working" before the merge pipeline is even active.
		const r = deriveReadiness(
			{ activity: { state: "active" }, status: "working" },
			[],
			smoke({ total: 2, pass: 1, fail: 1, checked: 2 }),
		);
		expect(r.verdict.word).toBe("Working");
		expect(tones(r).smoke).toBe("block"); // gate still shows the truth
	});

	it("a PR closed without merging → Closed", () => {
		const r = deriveReadiness({}, [pr({ state: "closed" })], smoke());
		expect(r.verdict.word).toBe("Closed");
		expect(r.verdict.hue).toBe("todo");
	});

	it("draft PR → Draft", () => {
		const r = deriveReadiness({ activity: { state: "idle" } }, [pr({ state: "draft" })], smoke());
		expect(r.verdict.word).toBe("Draft");
		expect(tones(r).pr).toBe("wait");
		expect(stateOf(r, "pr")).toBe("draft");
	});

	it("open PR, CI pending → Waiting on CI (blue)", () => {
		const r = deriveReadiness({}, [pr({ ci: { state: "pending", failingChecks: [] } })], smoke());
		expect(r.verdict.word).toBe("Waiting on CI");
		expect(r.verdict.hue).toBe("review");
		expect(r.currentKey).toBe("ci");
	});

	it("open PR, CI passing, review required → In Review (blue)", () => {
		const r = deriveReadiness(
			{},
			[pr({ review: { decision: "review_required", hasUnresolvedHumanComments: false, unresolvedBy: [] } })],
			smoke(),
		);
		expect(r.verdict.word).toBe("In Review");
		expect(r.verdict.hue).toBe("review");
	});

	it("changes requested → Changes Requested (red blocker) and Review gate blocks", () => {
		const r = deriveReadiness(
			{ status: "changes_requested" },
			[gl({ review: { decision: "changes_requested", hasUnresolvedHumanComments: true, unresolvedBy: [] } })],
			smoke(),
		);
		expect(r.verdict.word).toBe("Changes Requested");
		expect(r.verdict.hue).toBe("needs");
		expect(tones(r).review).toBe("block");
		expect(r.currentKey).toBe("review");
		expect(r.contextLabel).toBe("MR !3028 · open");
	});

	it("CI failing → CI Failing", () => {
		const r = deriveReadiness({}, [pr({ ci: { state: "failing", failingChecks: [] } })], smoke());
		expect(r.verdict.word).toBe("CI Failing");
		expect(tones(r).ci).toBe("block");
	});

	it("conflicting → Merge Conflict", () => {
		const r = deriveReadiness({}, [pr({ mergeability: { state: "conflicting", reasons: [], prUrl: "x" } })], smoke());
		expect(r.verdict.word).toBe("Merge Conflict");
		expect(tones(r).merge).toBe("block");
	});

	it("smoke failed → Smoke Failed", () => {
		const r = deriveReadiness(
			{},
			[pr({ review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] } })],
			smoke({ total: 3, pass: 2, fail: 1, checked: 3 }),
		);
		expect(r.verdict.word).toBe("Smoke Failed");
		expect(tones(r).smoke).toBe("block");
	});

	it("all gates green → Ready to Merge (green, pulsing)", () => {
		const r = deriveReadiness(
			{ status: "mergeable" },
			[pr({ review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] } })],
			smoke({ total: 2, pass: 2, checked: 2 }),
		);
		expect(r.verdict.word).toBe("Ready to Merge");
		expect(r.verdict.hue).toBe("merge");
		expect(r.verdict.pulse).toBe(true);
		expect(r.currentKey).toBeUndefined();
	});

	// Smoke's idle is deliberately non-blocking and must stay that way: a checklist
	// nobody authored means there was nothing to check, unlike a review nobody gave.
	// Tightening the Review gate is exactly the change that could take this with it.
	it("smoke never authored does NOT block Ready to Merge", () => {
		const r = deriveReadiness(
			{},
			[pr({ review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] } })],
			smoke({ total: 0 }),
		);
		expect(r.verdict.word).toBe("Ready to Merge");
		expect(r.verdict.pulse).toBe(true);
		expect(tones(r).smoke).toBe("idle");
		expect(stateOf(r, "smoke")).toBe("not run");
		expect(r.currentKey).toBeUndefined();
	});

	// The headline must never contradict the gate row beneath it. An open PR that
	// nobody has reviewed used to read "Ready to Merge — all gates pass" while its
	// own Review gate said "awaiting".
	it("open PR nobody has reviewed → Waiting on Review, not Ready to Merge", () => {
		const r = deriveReadiness({ status: "review_pending", activity: { state: "idle" } }, [pr()], smoke());
		expect(r.verdict.word).toBe("Waiting on Review");
		expect(r.verdict.hue).toBe("review");
		expect(r.verdict.pulse).toBeFalsy();
		expect(tones(r).review).toBe("wait");
		expect(stateOf(r, "review")).toBe("awaiting");
		expect(r.currentKey).toBe("review");
	});

	it("says nobody has looked rather than implying a review is under way", () => {
		const unreviewed = deriveReadiness({}, [pr()], smoke());
		const underway = deriveReadiness(
			{},
			[pr({ review: { decision: "review_required", hasUnresolvedHumanComments: false, unresolvedBy: [] } })],
			smoke(),
		);
		expect(unreviewed.verdict.caption).toBe("No one has reviewed this yet. Merging is your call.");
		expect(underway.verdict.word).toBe("In Review");
		// Same blue lane either way: neither posture blocks the human from merging.
		expect(unreviewed.verdict.hue).toBe(underway.verdict.hue);
	});

	// A PR with no review can only be idle when there is no PR at all, and that
	// route returns Working long before `ready` is evaluated.
	it("leaves Review idle only when there is no pull request to review", () => {
		const r = deriveReadiness({ status: "working", activity: { state: "active" } }, [], smoke());
		expect(tones(r).review).toBe("idle");
		expect(r.verdict.word).toBe("Working");
	});

	it("authored-but-pending smoke keeps it Waiting on Smoke, not Ready", () => {
		const r = deriveReadiness(
			{},
			[pr({ review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] } })],
			smoke({ total: 3, pass: 1, pending: 2, checked: 1 }),
		);
		expect(r.verdict.word).toBe("Waiting on Smoke");
		expect(tones(r).smoke).toBe("wait");
	});

	it("merged PR → Merged (terminal, no pulse)", () => {
		const r = deriveReadiness({ status: "merged" }, [pr({ state: "merged" })], smoke());
		expect(r.verdict.word).toBe("Merged");
		expect(r.verdict.hue).toBe("merge");
		expect(r.verdict.pulse).toBeUndefined();
		expect(tones(r).pr).toBe("pass");
		expect(tones(r).merge).toBe("pass");
	});
});

describe("deriveReadiness — pipeline order (smoke-before-PR)", () => {
	it("gates render Work → Smoke → PR → CI → Review → Merge", () => {
		const r = deriveReadiness({}, [pr()], smoke());
		expect(r.gates.map((g) => g.key)).toEqual(["work", "smoke", "pr", "ci", "review", "merge"]);
	});

	it("Smoke sits before PR in the strip", () => {
		const r = deriveReadiness({}, [pr()], smoke());
		const keys = r.gates.map((g) => g.key);
		expect(keys.indexOf("smoke")).toBeLessThan(keys.indexOf("pr"));
	});

	it("authored-but-pending smoke with no PR lights Smoke, not PR (current advances to the live gate)", () => {
		// Smoke is authored before the PR is opened. A pre-PR session that has an
		// in-flight smoke check should ring Smoke — the earliest live gate — while
		// PR/CI/Review/Merge sit idle downstream.
		const r = deriveReadiness(
			{ activity: { state: "idle" } },
			[],
			smoke({ total: 2, pass: 0, pending: 2, checked: 0 }),
		);
		expect(r.currentKey).toBe("smoke");
		expect(tones(r).smoke).toBe("wait");
		expect(tones(r).pr).toBe("idle");
		expect(stateOf(r, "smoke")).toBe("running");
	});

	it("passed smoke with no PR leaves no gate ringed (no false PR/CI highlight)", () => {
		const r = deriveReadiness({ activity: { state: "idle" } }, [], smoke({ total: 2, pass: 2, checked: 2 }));
		expect(tones(r).smoke).toBe("pass");
		// Work + Smoke are green; PR/CI/Review/Merge are idle (no PR yet) — nothing
		// is blocking or waiting, so no gate is current.
		expect(r.currentKey).toBeUndefined();
	});
});

describe("deriveReadiness — priority + gate independence", () => {
	it("Changes Requested outranks a CI failure in the headline, but BOTH gates stay red", () => {
		const r = deriveReadiness(
			{},
			[
				pr({
					ci: { state: "failing", failingChecks: [] },
					review: { decision: "changes_requested", hasUnresolvedHumanComments: false, unresolvedBy: [] },
				}),
			],
			smoke(),
		);
		expect(r.verdict.word).toBe("Changes Requested");
		expect(tones(r).review).toBe("block");
		expect(tones(r).ci).toBe("block"); // gates are independent facts — both surfaced
	});

	it("the most-actionable PR (open) wins over a merged sibling", () => {
		const r = deriveReadiness(
			{},
			[
				pr({ number: 8, state: "open", ci: { state: "pending", failingChecks: [] } }),
				pr({ number: 7, state: "merged" }),
			],
			smoke(),
		);
		expect(r.verdict.word).toBe("Waiting on CI");
		expect(r.contextLabel).toBe("PR #8 · open");
	});
});

describe("deriveReadiness — Review gate approval progress", () => {
	const live = { activity: { state: "idle" as const }, status: "review_pending" as const };

	it("shows the fraction and stays amber (wait) while short of the threshold", () => {
		const r = deriveReadiness(
			live,
			[
				gl({
					review: {
						decision: "none",
						hasUnresolvedHumanComments: false,
						unresolvedBy: [],
						approvalRuleSource: "ao",
						approvalsCount: 1,
						requiredApprovals: 2,
					},
				}),
			],
			smoke(),
		);
		expect(tones(r).review).toBe("wait");
		expect(stateOf(r, "review")).toBe("1/2 approved");
		expect(r.verdict.word).toBe("In Review");
	});

	it("turns green (pass) once the threshold is met", () => {
		const r = deriveReadiness(
			live,
			[
				gl({
					review: {
						decision: "none",
						hasUnresolvedHumanComments: false,
						unresolvedBy: [],
						approvalRuleSource: "ao",
						approvalsCount: 2,
						requiredApprovals: 2,
					},
				}),
			],
			smoke(),
		);
		expect(tones(r).review).toBe("pass");
		expect(stateOf(r, "review")).toBe("2/2 approved");
	});

	it("keeps the honest count when over the threshold", () => {
		const r = deriveReadiness(
			live,
			[
				gl({
					review: {
						decision: "approved",
						hasUnresolvedHumanComments: false,
						unresolvedBy: [],
						approvalRuleSource: "scm",
						approvalsCount: 3,
						requiredApprovals: 2,
					},
				}),
			],
			smoke(),
		);
		expect(tones(r).review).toBe("pass");
		expect(stateOf(r, "review")).toBe("3/2 approved");
	});

	it("lets changes-requested win over approval progress", () => {
		const r = deriveReadiness(
			live,
			[
				gl({
					review: {
						decision: "changes_requested",
						hasUnresolvedHumanComments: true,
						unresolvedBy: [],
						approvalRuleSource: "ao",
						approvalsCount: 1,
						requiredApprovals: 2,
					},
				}),
			],
			smoke(),
		);
		expect(tones(r).review).toBe("block");
		expect(stateOf(r, "review")).toBe("changes");
	});

	it("falls back to the decision label when no rule applies", () => {
		const r = deriveReadiness(
			live,
			[pr({ review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] } })],
			smoke(),
		);
		expect(tones(r).review).toBe("pass");
		expect(stateOf(r, "review")).toBe("approved");
	});
});

// The false reassurance behind the incident this branch fixes: a worker that had
// stopped mid-task still headlined "Working — agent is still working", so 14
// minutes of nothing looked exactly like 14 minutes of thinking.
describe("a session that stopped before opening a PR", () => {
	it("headlines that it stopped, not that the agent is still working", () => {
		const readiness = deriveReadiness(
			{
				status: "terminated",
				activity: { state: "exited" },
				termination: { source: "agent", reason: "other", at: "2026-08-17T09:36:05Z" },
			},
			[],
			smoke(),
		);
		expect(readiness.verdict.word).toBe("Stopped");
		expect(readiness.verdict.caption).toContain("agent");
		expect(readiness.verdict.pulse).toBeFalsy();
	});

	it("says so when AO was the one that ended it", () => {
		const readiness = deriveReadiness(
			{
				status: "terminated",
				activity: { state: "exited" },
				termination: { source: "ao", reason: "auto_reclaim", at: "2026-08-17T09:36:05Z" },
			},
			[],
			smoke(),
		);
		expect(readiness.verdict.word).toBe("Stopped");
		expect(readiness.verdict.caption).toContain("AO");
	});

	// A live session with no PR is genuinely still working — the fix must not
	// turn every pre-PR session into a stopped one.
	it("still reads as working while the session is live", () => {
		const readiness = deriveReadiness({ status: "working", activity: { state: "active" } }, [], smoke());
		expect(readiness.verdict.word).toBe("Working");
	});

	// A session that ended AFTER its work merged is finished, not stopped: the
	// merged verdict is the more useful headline.
	it("keeps the merged verdict for a terminated session whose PR merged", () => {
		const readiness = deriveReadiness(
			{
				status: "merged",
				activity: { state: "exited" },
				termination: { source: "ao", reason: "work_complete", at: "2026-08-17T09:36:05Z" },
			},
			[pr({ state: "merged" })],
			smoke(),
		);
		expect(readiness.verdict.word).toBe("Merged");
	});
});

// The Work gate is the pipeline's first claim about what happened. Calling it
// "done" on a session that stopped without producing a pull request tells the
// reader the work landed, which is the opposite of what occurred.
describe("the Work gate on a session that stopped with nothing to show", () => {
	it("does not claim the work is done", () => {
		const r = deriveReadiness({ status: "terminated", activity: { state: "exited" } }, [], smoke());
		const work = r.gates.find((g) => g.key === "work")!;
		expect(work.state).toBe("stopped");
		expect(work.tone).toBe("block");
	});

	it("still reads done once a pull request exists, however the session ended", () => {
		const r = deriveReadiness(
			{ status: "terminated", activity: { state: "exited" } },
			[pr({ state: "open" })],
			smoke(),
		);
		expect(r.gates.find((g) => g.key === "work")!.state).toBe("done");
	});
});

// AO's own reviewer, folded into the Review gate. Before this the gate read only
// the provider's decision, so an AO approval was invisible on the board no matter
// how durably it was recorded.
describe("deriveReadiness — AO's own review verdict", () => {
	const aoApproved = { verdict: "approved" as const, runId: "run-1", targetSha: "abc123", reviewedAt: "2026-06-15T00:00:00Z" };
	const aoChanges = {
		verdict: "changes_requested" as const,
		runId: "run-1",
		targetSha: "abc123",
		reviewedAt: "2026-06-15T00:00:00Z",
	};

	it("an AO approval answers 'nobody has looked' and turns the gate green", () => {
		const r = deriveReadiness({ status: "review_pending", activity: { state: "idle" } }, [pr({ aoReview: aoApproved })], smoke());
		expect(tones(r).review).toBe("pass");
		expect(stateOf(r, "review")).toBe("AO approved");
		expect(r.verdict.word).toBe("Ready to Merge");
	});

	it("AO requesting changes blocks a review the provider left quiet", () => {
		const r = deriveReadiness({}, [pr({ aoReview: aoChanges })], smoke());
		expect(tones(r).review).toBe("block");
		expect(r.verdict.word).toBe("Changes Requested");
	});

	// The approval rule is about human approvers on the forge. AO is not one of
	// them, so an explicit review_required keeps its own label and its own tone.
	it("an AO approval does not satisfy an explicit review_required", () => {
		const r = deriveReadiness(
			{},
			[pr({ review: { decision: "review_required", hasUnresolvedHumanComments: false, unresolvedBy: [] }, aoReview: aoApproved })],
			smoke(),
		);
		expect(tones(r).review).toBe("wait");
		expect(stateOf(r, "review")).toBe("required");
		expect(r.verdict.word).toBe("In Review");
	});

	it("an AO approval does not satisfy a numeric approval threshold", () => {
		const r = deriveReadiness(
			{},
			[
				pr({
					review: {
						decision: "none",
						hasUnresolvedHumanComments: false,
						unresolvedBy: [],
						approvalsCount: 0,
						requiredApprovals: 2,
						approvalRuleSource: "scm",
					},
					aoReview: aoApproved,
				}),
			],
			smoke(),
		);
		expect(tones(r).review).toBe("wait");
		expect(stateOf(r, "review")).toBe("0/2 approved");
	});

	// Preservation: with no AO verdict the gate reads exactly as it did before.
	it("leaves the gate alone when AO has not reviewed", () => {
		const r = deriveReadiness({ status: "review_pending", activity: { state: "idle" } }, [pr()], smoke());
		expect(tones(r).review).toBe("wait");
		expect(stateOf(r, "review")).toBe("awaiting");
	});
});
