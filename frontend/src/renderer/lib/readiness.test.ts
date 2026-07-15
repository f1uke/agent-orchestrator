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

	it("smoke never authored does NOT block Ready to Merge", () => {
		const r = deriveReadiness(
			{},
			[pr({ review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] } })],
			smoke({ total: 0 }),
		);
		expect(r.verdict.word).toBe("Ready to Merge");
		expect(tones(r).smoke).toBe("idle");
		expect(stateOf(r, "smoke")).toBe("not run");
	});

	it("GitHub PR mergeable with no review → Ready to Merge (review idle doesn't block)", () => {
		const r = deriveReadiness({}, [pr()], smoke());
		expect(r.verdict.word).toBe("Ready to Merge");
		expect(tones(r).review).toBe("idle");
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
