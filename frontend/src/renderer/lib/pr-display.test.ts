import { describe, expect, it } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import {
	type ApprovalProgress,
	approvalLabel,
	approvalProgress,
	isArchivedPRState,
	prBrowserUrl,
	prDiffSummary,
	prKindLabel,
	prNoun,
	prNounPlural,
	prRef,
	prStatusRows,
	prSummaryParts,
	prTitleLabel,
	providerFromPRURL,
} from "./pr-display";

const summary = (overrides: Partial<SessionPRSummary> = {}): SessionPRSummary => ({
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
	review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] },
	mergeability: { state: "mergeable", reasons: [], prUrl: "https://github.com/acme/repo/pull/7" },
	updatedAt: "2026-06-15T00:00:00Z",
	observedAt: "2026-06-15T00:00:00Z",
	ciObservedAt: "2026-06-15T00:00:00Z",
	reviewObservedAt: "2026-06-15T00:00:00Z",
	...overrides,
});

describe("prStatusRows", () => {
	it("formats the three PR states without exposing raw unknown", () => {
		const rows = prStatusRows(
			summary({
				ci: { state: "unknown", failingChecks: [] },
				review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
				mergeability: { state: "unknown", reasons: [], prUrl: "https://github.com/acme/repo/pull/7" },
			}),
		);

		expect(rows.map((row) => `${row.label}:${row.value}`)).toEqual(["CI:Checking", "Merge:Checking", "Review:None"]);
	});

	it("includes minimal diff detail on the merge row", () => {
		const rows = prStatusRows(summary({ changedFiles: 4, additions: 25, deletions: 2 }));
		expect(rows.find((row) => row.key === "merge")?.detail).toBe("4 files");
	});
});

describe("prDiffSummary", () => {
	it("formats file and line delta metadata", () => {
		expect(prDiffSummary(summary({ changedFiles: 6, additions: 42, deletions: 8 }))).toBe("6 files · +42 -8");
	});

	it("omits the diff label when no diff metadata is available", () => {
		expect(prDiffSummary(summary({ changedFiles: 0, additions: 0, deletions: 0 }))).toBeUndefined();
	});
});

describe("prBrowserUrl", () => {
	it("normalizes issue-shaped GitHub PR URLs to the pull request page", () => {
		expect(
			prBrowserUrl(
				summary({
					url: "https://github.com/acme/repo/issues/7",
					htmlUrl: "https://github.com/acme/repo/issues/7",
				}),
			),
		).toBe("https://github.com/acme/repo/pull/7");
	});

	it("normalizes a GitLab MR URL (nested group, sub-tab, query) to the canonical MR page", () => {
		expect(
			prBrowserUrl(
				summary({
					provider: "gitlab",
					url: "https://gitlab.finnomena.com/group/sub/proj/-/merge_requests/42/diffs?tab=x",
					htmlUrl: "https://gitlab.finnomena.com/group/sub/proj/-/merge_requests/42/diffs?tab=x",
				}),
			),
		).toBe("https://gitlab.finnomena.com/group/sub/proj/-/merge_requests/42");
	});
});

describe("provider-aware PR/MR labels", () => {
	it("abbreviates the change-request kind per provider", () => {
		expect(prKindLabel("github")).toBe("PR");
		expect(prKindLabel("gitlab")).toBe("MR");
	});

	it("uses # for GitHub refs and ! for GitLab refs", () => {
		expect(prRef("github", 42)).toBe("#42");
		expect(prRef("gitlab", 42)).toBe("!42");
	});

	it("combines kind and ref into a title label", () => {
		expect(prTitleLabel("github", 42)).toBe("PR #42");
		expect(prTitleLabel("gitlab", 42)).toBe("MR !42");
	});

	it("spells out the provider-specific noun", () => {
		expect(prNoun("github")).toBe("pull request");
		expect(prNoun("gitlab")).toBe("merge request");
		expect(prNounPlural("gitlab")).toBe("merge requests");
	});
});

describe("providerFromPRURL", () => {
	it("detects a GitLab merge request URL by its path marker", () => {
		expect(providerFromPRURL("https://gitlab.finnomena.com/group/sub/proj/-/merge_requests/42")).toBe("gitlab");
	});

	it("defaults to github for a pull URL", () => {
		expect(providerFromPRURL("https://github.com/acme/repo/pull/7")).toBe("github");
	});
});

describe("GitLab MR conflict link", () => {
	it("points at the merge request conflicts subpage", () => {
		const parts = prSummaryParts(
			summary({
				provider: "gitlab",
				url: "https://gitlab.finnomena.com/group/proj/-/merge_requests/9",
				htmlUrl: "https://gitlab.finnomena.com/group/proj/-/merge_requests/9",
				mergeability: {
					state: "conflicting",
					reasons: [],
					prUrl: "https://gitlab.finnomena.com/group/proj/-/merge_requests/9",
					conflictFiles: [],
				},
			}),
		);
		const merge = parts.find((part) => part.key === "merge");
		expect(merge?.links[0]?.href).toBe("https://gitlab.finnomena.com/group/proj/-/merge_requests/9/conflicts");
	});
});

describe("prSummaryParts", () => {
	it("always returns CI, Merge, and Review parts", () => {
		expect(prSummaryParts(summary()).map((part) => part.label)).toEqual(["CI", "Merge", "Review"]);
	});

	it("details active CI, merge, and review blockers under their parts", () => {
		const parts = prSummaryParts(
			summary({
				ci: {
					state: "failing",
					failingChecks: [
						{ name: "copy-check", status: "failed", conclusion: "failure", url: "https://checks.example/copy" },
					],
				},
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: true,
					unresolvedBy: [
						{
							reviewerId: "alice",
							count: 6,
							links: [{ url: "https://github.com/acme/repo/pull/7#discussion_r1", file: "main.go", line: 12 }],
						},
					],
				},
				mergeability: {
					state: "blocked",
					reasons: ["behind_base"],
					prUrl: "https://github.com/acme/repo/pull/7",
				},
			}),
		);

		expect(parts.map((part) => part.key)).toEqual(["ci", "merge", "review"]);
		expect(parts.find((part) => part.key === "ci")).toMatchObject({
			status: "Failing",
			summary: undefined,
			tone: "error",
		});
		expect(parts.find((part) => part.key === "ci")?.links[0]).toMatchObject({
			label: "copy-check",
			href: "https://checks.example/copy",
		});
		expect(parts.find((part) => part.key === "merge")).toMatchObject({
			status: "Blocked",
			summary: undefined,
			tone: "warning",
		});
		expect(parts.find((part) => part.key === "review")).toMatchObject({
			status: "Changes requested",
			summary: undefined,
			tone: "warning",
		});
		expect(parts.find((part) => part.key === "review")?.links[0]).toMatchObject({
			label: "alice +5",
			href: "https://github.com/acme/repo/pull/7#discussion_r1",
		});
	});

	it("links failing CI checks to their provider URLs", () => {
		const parts = prSummaryParts(
			summary({
				ci: {
					state: "failing",
					failingChecks: [
						{ name: "unit", status: "failed", conclusion: "failure", url: "https://checks.example/unit" },
						{ name: "lint", status: "failed", conclusion: "failure", url: "https://checks.example/lint" },
						{ name: "build", status: "failed", conclusion: "failure", url: "https://checks.example/build" },
						{ name: "types", status: "failed", conclusion: "failure", url: "https://checks.example/types" },
					],
				},
			}),
		);

		const ciPart = parts.find((part) => part.key === "ci");
		expect(ciPart?.links).toEqual([
			{ label: "unit", href: "https://checks.example/unit", title: "failure" },
			{ label: "lint", href: "https://checks.example/lint", title: "failure" },
			{ label: "build", href: "https://checks.example/build", title: "failure" },
		]);
		expect(ciPart?.overflowLabel).toBe("+1 check");
	});

	it("prefers the submitted review summary over inline comments", () => {
		const parts = prSummaryParts(
			summary({
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: true,
					unresolvedBy: [
						{
							reviewerId: "alice",
							count: 2,
							reviewUrl: "https://github.com/acme/repo/pull/7#pullrequestreview-1",
							links: [
								{ url: "https://github.com/acme/repo/pull/7#discussion_r1", file: "main.go", line: 12 },
								{ url: "https://github.com/acme/repo/pull/7#discussion_r2", file: "test.go", line: 20 },
							],
						},
					],
				},
			}),
		);

		expect(parts.find((part) => part.key === "review")?.links[0]).toMatchObject({
			label: "alice +1",
			href: "https://github.com/acme/repo/pull/7#pullrequestreview-1",
			title: "Open requested-changes review from alice",
		});
	});

	it("falls back to the first inline comment when no review summary exists", () => {
		const parts = prSummaryParts(
			summary({
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: true,
					unresolvedBy: [
						{
							reviewerId: "alice",
							count: 2,
							links: [
								{ url: "https://github.com/acme/repo/pull/7#discussion_r1", file: "main.go", line: 12 },
								{ url: "https://github.com/acme/repo/pull/7#discussion_r2", file: "test.go", line: 20 },
							],
						},
					],
				},
			}),
		);

		expect(parts.find((part) => part.key === "review")?.links[0]).toMatchObject({
			label: "alice +1",
			href: "https://github.com/acme/repo/pull/7#discussion_r1",
			title: "2 unresolved comments from alice",
		});
	});

	it("falls back to the PR page when review summary and inline comment URLs are missing", () => {
		const parts = prSummaryParts(
			summary({
				url: "https://github.com/acme/repo/issues/7",
				htmlUrl: "https://github.com/acme/repo/issues/7",
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: true,
					unresolvedBy: [{ reviewerId: "alice", count: 1, links: [] }],
				},
			}),
		);

		expect(parts.find((part) => part.key === "review")?.links[0]).toMatchObject({
			label: "alice",
			href: "https://github.com/acme/repo/pull/7",
			title: "Open pull request for alice",
		});
	});

	it("shows bot reviewers with a bot label", () => {
		const parts = prSummaryParts(
			summary({
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: false,
					unresolvedBy: [
						{
							reviewerId: "copilot",
							count: 0,
							reviewUrl: "https://github.com/acme/repo/pull/7#pullrequestreview-2",
							isBot: true,
							links: [],
						},
					],
				},
			}),
		);

		expect(parts.find((part) => part.key === "review")?.links[0]).toMatchObject({
			label: "copilot bot",
			href: "https://github.com/acme/repo/pull/7#pullrequestreview-2",
			title: "Open requested-changes review from copilot bot",
		});
	});

	it("links merge conflicts to GitHub's conflict resolution page", () => {
		const parts = prSummaryParts(
			summary({
				url: "https://github.com/acme/repo/issues/7",
				htmlUrl: "https://github.com/acme/repo/issues/7",
				mergeability: {
					state: "conflicting",
					reasons: [],
					prUrl: "https://github.com/acme/repo/issues/7",
				},
			}),
		);

		expect(parts.find((part) => part.key === "merge")).toMatchObject({
			status: "Conflict",
			summary: undefined,
		});
		expect(parts.find((part) => part.key === "merge")?.links[0]).toMatchObject({
			label: "conflicts",
			href: "https://github.com/acme/repo/pull/7/conflicts",
		});
	});

	it("keeps closed or merged PR summaries to the three status parts", () => {
		const parts = prSummaryParts(
			summary({
				state: "merged",
				ci: { state: "failing", failingChecks: [{ name: "unit", status: "failed", conclusion: "failure" }] },
				review: { decision: "changes_requested", hasUnresolvedHumanComments: true, unresolvedBy: [] },
				mergeability: { state: "conflicting", reasons: ["conflicts"], prUrl: "https://github.com/acme/repo/pull/7" },
			}),
		);

		expect(parts).toHaveLength(3);
		expect(parts.find((part) => part.key === "merge")?.links).toEqual([]);
		expect(parts.find((part) => part.key === "review")?.links).toEqual([]);
	});

	it("puts draft readiness under Review", () => {
		const parts = prSummaryParts(
			summary({ state: "draft", review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] } }),
		);

		expect(parts.find((part) => part.key === "review")).toMatchObject({
			status: "None",
			summary: "Draft PR · Not ready for review",
		});
	});
});

describe("isArchivedPRState", () => {
	it("treats merged and closed as archived, open and draft as active", () => {
		expect(isArchivedPRState("merged")).toBe(true);
		expect(isArchivedPRState("closed")).toBe(true);
		expect(isArchivedPRState("open")).toBe(false);
		expect(isArchivedPRState("draft")).toBe(false);
	});
});

describe("approvalProgress", () => {
	const review = (
		over: Partial<SessionPRSummary["review"]> = {},
	): SessionPRSummary["review"] => ({
		decision: "none",
		hasUnresolvedHumanComments: false,
		unresolvedBy: [],
		...over,
	});

	it("returns null when no rule applies (source none / absent)", () => {
		expect(approvalProgress(review({ approvalRuleSource: "none", approvalsCount: 1 }))).toBeNull();
		expect(approvalProgress(review())).toBeNull();
	});

	it("reports a shortfall while under the AO threshold", () => {
		expect(approvalProgress(review({ approvalRuleSource: "ao", approvalsCount: 1, requiredApprovals: 2 }))).toEqual({
			approved: 1,
			required: 2,
			remaining: 1,
			met: false,
			source: "ao",
		});
	});

	it("marks met at the threshold with no remaining", () => {
		const got = approvalProgress(review({ approvalRuleSource: "ao", approvalsCount: 2, requiredApprovals: 2 }));
		expect(got).toMatchObject({ approved: 2, required: 2, remaining: 0, met: true });
	});

	it("keeps the honest count when over the threshold", () => {
		const got = approvalProgress(review({ approvalRuleSource: "scm", approvalsCount: 3, requiredApprovals: 2 }));
		expect(got).toMatchObject({ approved: 3, required: 2, remaining: 0, met: true, source: "scm" });
	});

	it("degrades to count-only when an SCM rule exposes no numeric threshold", () => {
		const got = approvalProgress(review({ approvalRuleSource: "scm", approvalsCount: 3 }));
		expect(got).toMatchObject({ approved: 3, required: null, remaining: 0, met: false, source: "scm" });
	});

	it("defaults an absent count to zero", () => {
		const got = approvalProgress(review({ approvalRuleSource: "ao", requiredApprovals: 2 }));
		expect(got).toMatchObject({ approved: 0, required: 2, remaining: 2, met: false });
	});
});

describe("prSummaryParts — approval progress (Review row)", () => {
	const glReview = (review: Partial<SessionPRSummary["review"]>) =>
		prSummaryParts(
			summary({
				provider: "gitlab",
				url: "https://gitlab.com/acme/repo/-/merge_requests/3",
				htmlUrl: "https://gitlab.com/acme/repo/-/merge_requests/3",
				review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [], ...review },
			}),
		).find((part) => part.key === "review");

	it("shows the meter, fraction, and shortfall while short", () => {
		expect(glReview({ approvalRuleSource: "ao", approvalsCount: 1, requiredApprovals: 2 })).toMatchObject({
			status: "1/2 approved",
			summary: "1 more needed",
			tone: "neutral",
			approval: { approved: 1, required: 2, met: false },
		});
	});

	it("turns success with no shortfall once met", () => {
		expect(glReview({ approvalRuleSource: "ao", approvalsCount: 2, requiredApprovals: 2 })).toMatchObject({
			status: "2/2 approved",
			tone: "success",
			approval: { met: true },
		});
		expect(glReview({ approvalRuleSource: "ao", approvalsCount: 2, requiredApprovals: 2 })?.summary).toBeUndefined();
	});

	it("lets changes-requested keep its own label over progress", () => {
		expect(
			glReview({ decision: "changes_requested", approvalRuleSource: "ao", approvalsCount: 1, requiredApprovals: 2 }),
		).toMatchObject({ status: "Changes requested", tone: "warning" });
	});

	it("keeps today's decision label when no rule applies", () => {
		const part = prSummaryParts(summary({ review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] } })).find(
			(p) => p.key === "review",
		);
		expect(part).toMatchObject({ status: "Approved" });
		expect(part?.approval).toBeFalsy();
	});
});

describe("approvalLabel", () => {
	const p = (over: Partial<ApprovalProgress>): ApprovalProgress => ({
		approved: 1,
		required: 2,
		remaining: 1,
		met: false,
		source: "ao",
		...over,
	});

	it("shows the fraction plus a plain-language shortfall", () => {
		expect(approvalLabel(p({}), { remaining: true })).toBe("1/2 approved · 1 more needed");
		expect(approvalLabel(p({ approved: 0, remaining: 2 }), { remaining: true })).toBe(
			"0/2 approved · 2 more needed",
		);
	});

	it("omits the shortfall when not requested", () => {
		expect(approvalLabel(p({}))).toBe("1/2 approved");
	});

	it("drops the shortfall once met", () => {
		expect(approvalLabel(p({ approved: 2, remaining: 0, met: true }), { remaining: true })).toBe("2/2 approved");
	});

	it("keeps the honest count when over threshold", () => {
		expect(approvalLabel(p({ approved: 3, remaining: 0, met: true }), { remaining: true })).toBe("3/2 approved");
	});

	it("renders count-only when the threshold is unknown", () => {
		expect(approvalLabel(p({ approved: 3, required: null, source: "scm" }), { remaining: true })).toBe("3 approved");
	});
});
