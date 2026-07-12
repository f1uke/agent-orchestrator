import { describe, expect, it } from "vitest";
import {
	attentionZone,
	canonicalTrackerIssueId,
	findProjectOrchestrator,
	formatNextTransition,
	idleCountdown,
	IDLE_COUNTDOWN_THRESHOLD_MS,
	newestActiveOrchestrator,
	orchestratorHealth,
	projectRowActive,
	sessionIsActive,
	sessionNeedsAttention,
	statusReasonLabel,
	toAgentProvider,
	toSessionStatus,
	toStatusReason,
	workerDisplayStatus,
	workerStatusPulses,
	openPRs,
	mergedPRCount,
	isMergeSuspended,
	mergedSuspendPRNumber,
	primaryPR,
	sortedPRs,
	type AttentionZone,
	type PRState,
	type PullRequestFacts,
	type SessionStatus,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "./workspace";

describe("canonicalTrackerIssueId", () => {
	it("keeps provider-prefixed intake ids and rejects manual task titles", () => {
		expect(canonicalTrackerIssueId("github:acme/project#42")).toBe("github:acme/project#42");
		expect(canonicalTrackerIssueId("gitlab:group/sub/project#42")).toBe("gitlab:group/sub/project#42");
		expect(canonicalTrackerIssueId("Fix fallback renderer")).toBeUndefined();
		expect(canonicalTrackerIssueId(undefined)).toBeUndefined();
	});
});

function sessionWith(overrides: Partial<WorkspaceSession>): WorkspaceSession {
	return {
		id: "sess-1",
		workspaceId: "ws-1",
		workspaceName: "my-app",
		title: "fix-bug",
		provider: "claude-code",
		branch: "feat/x",
		status: "working",
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		...overrides,
	};
}

const pr = (overrides: Partial<PullRequestFacts> & { number: number; state: PRState }): PullRequestFacts => ({
	url: `https://example.com/pr/${overrides.number}`,
	ci: "passing",
	review: "approved",
	mergeability: "mergeable",
	reviewComments: false,
	updatedAt: "2026-01-01T00:00:00Z",
	...overrides,
});

describe("idleCountdown", () => {
	const now = Date.parse("2026-01-01T12:00:00Z");
	const at = (msFromNow: number) => new Date(now + msFromNow).toISOString();

	const H = 60 * 60_000;

	it("returns null far from expiry (beyond the threshold)", () => {
		const session = sessionWith({ idleCloseAt: at(IDLE_COUNTDOWN_THRESHOLD_MS + 2 * H) }); // 26h out
		expect(idleCountdown(session, now)).toBeNull();
	});

	it("shows a 'soon' countdown within a day of expiry", () => {
		const session = sessionWith({ idleCloseAt: at(20 * H) }); // 20h out
		const c = idleCountdown(session, now);
		expect(c?.level).toBe("soon");
		expect(c?.label).toBe("20h");
	});

	it("escalates to 'urgent' at ≤6h", () => {
		const session = sessionWith({ idleCloseAt: at(5 * H) });
		const c = idleCountdown(session, now);
		expect(c?.level).toBe("urgent");
		expect(c?.label).toBe("5h");
	});

	it("escalates to 'imminent' at ≤1h", () => {
		const session = sessionWith({ idleCloseAt: at(45_000) });
		const c = idleCountdown(session, now);
		expect(c?.level).toBe("imminent");
		expect(c?.label).toBe("45s");
	});

	it("returns null once the deadline has passed", () => {
		const session = sessionWith({ idleCloseAt: at(-1000) });
		expect(idleCountdown(session, now)).toBeNull();
	});

	it("returns null for a suspended session (it shows a paused affordance instead)", () => {
		const session = sessionWith({ isSuspended: true, idleCloseAt: at(5 * 60_000) });
		expect(idleCountdown(session, now)).toBeNull();
	});

	it("returns null when no deadline is set", () => {
		expect(idleCountdown(sessionWith({}), now)).toBeNull();
	});
});

describe("toSessionStatus", () => {
	it("passes through a known status", () => {
		expect(toSessionStatus("mergeable")).toBe("mergeable");
		expect(toSessionStatus("no_signal")).toBe("no_signal");
	});

	it("keeps a backend merged status even when the session is terminated", () => {
		expect(toSessionStatus("merged", true)).toBe("merged");
	});

	it("uses terminated only as a fallback when a terminated session has no known status", () => {
		expect(toSessionStatus(undefined, true)).toBe("terminated");
	});

	it("falls back to unknown for an unknown live status", () => {
		expect(toSessionStatus("bogus")).toBe("unknown");
		expect(toSessionStatus(undefined)).toBe("unknown");
	});
});

describe("workerDisplayStatus", () => {
	it("prefers an explicit displayStatus override", () => {
		expect(workerDisplayStatus(sessionWith({ status: "ci_failed", displayStatus: "done" }))).toBe("done");
	});

	it.each([
		["needs_input", "needs_you"],
		["changes_requested", "needs_you"],
		["review_pending", "needs_you"],
		["ci_failed", "ci_failed"],
		["no_signal", "no_signal"],
		["approved", "mergeable"],
		["mergeable", "mergeable"],
		["merged", "done"],
		["terminated", "done"],
		["unknown", "unknown"],
		["working", "working"],
		["idle", "working"],
	] as const)("maps %s to %s", (status, expected) => {
		expect(workerDisplayStatus(sessionWith({ status }))).toBe(expected);
	});
});

describe("sessionIsActive", () => {
	it("is false for merged and terminated", () => {
		expect(sessionIsActive(sessionWith({ status: "merged" }))).toBe(false);
		expect(sessionIsActive(sessionWith({ status: "terminated" }))).toBe(false);
	});

	it("is true for in-progress statuses", () => {
		expect(sessionIsActive(sessionWith({ status: "working" }))).toBe(true);
		expect(sessionIsActive(sessionWith({ status: "pr_open" }))).toBe(true);
	});
});

describe("findProjectOrchestrator", () => {
	function workspaceWith(sessions: WorkspaceSession[]): WorkspaceSummary {
		return { id: "skills", name: "skills", path: "/tmp/skills", sessions };
	}

	it("skips a terminated orchestrator that precedes the live one", () => {
		// Regression: the daemon lists sessions by spawn number, so a dead
		// orchestrator (zellij session deleted) sorts before its live successor.
		// Picking it sent the Orchestrator button to an instant "[process exited]".
		const dead = sessionWith({ id: "skills-4", kind: "orchestrator", status: "terminated" });
		const live = sessionWith({ id: "skills-5", kind: "orchestrator", status: "needs_input" });
		const worker = sessionWith({ id: "skills-6", kind: "worker", status: "working" });
		expect(findProjectOrchestrator([workspaceWith([dead, live, worker])], "skills")).toBe(live);
	});

	it("prefers the newest live orchestrator when multiple replacements overlap", () => {
		const older = sessionWith({ id: "skills-4", kind: "orchestrator", status: "idle", provider: "claude-code" });
		const newer = sessionWith({ id: "skills-5", kind: "orchestrator", status: "working", provider: "codex" });
		expect(findProjectOrchestrator([workspaceWith([older, newer])], "skills")).toBe(newer);
	});

	it("returns undefined when every orchestrator is terminated", () => {
		const dead = sessionWith({ id: "skills-4", kind: "orchestrator", status: "terminated" });
		expect(findProjectOrchestrator([workspaceWith([dead])], "skills")).toBeUndefined();
	});

	it("ignores live workers when looking for an orchestrator", () => {
		const worker = sessionWith({ id: "skills-6", kind: "worker", status: "working" });
		expect(findProjectOrchestrator([workspaceWith([worker])], "skills")).toBeUndefined();
	});

	it("returns undefined for an unknown project", () => {
		const live = sessionWith({ id: "skills-5", kind: "orchestrator", status: "working" });
		expect(findProjectOrchestrator([workspaceWith([live])], "other")).toBeUndefined();
	});

	it("selects the newest active orchestrator, not the first active one", () => {
		const older = sessionWith({
			id: "skills-1",
			kind: "orchestrator",
			status: "working",
			createdAt: "2026-01-01T00:00:00Z",
			updatedAt: "2026-01-01T00:00:00Z",
		});
		const newer = sessionWith({
			id: "skills-2",
			kind: "orchestrator",
			status: "working",
			createdAt: "2026-01-02T00:00:00Z",
			updatedAt: "2026-01-02T00:00:00Z",
		});
		expect(findProjectOrchestrator([workspaceWith([older, newer])], "skills")).toBe(newer);
	});

	it("uses updatedAt and id as newest orchestrator tie breakers", () => {
		const oldUpdate = sessionWith({
			id: "skills-2",
			kind: "orchestrator",
			status: "working",
			createdAt: "2026-01-01T00:00:00Z",
			updatedAt: "2026-01-01T00:00:00Z",
		});
		const newUpdate = sessionWith({
			id: "skills-1",
			kind: "orchestrator",
			status: "working",
			createdAt: "2026-01-01T00:00:00Z",
			updatedAt: "2026-01-02T00:00:00Z",
		});
		const sameTimesHigherID = sessionWith({
			id: "skills-3",
			kind: "orchestrator",
			status: "working",
			createdAt: "2026-01-01T00:00:00Z",
			updatedAt: "2026-01-02T00:00:00Z",
		});
		expect(newestActiveOrchestrator([oldUpdate, newUpdate])).toBe(newUpdate);
		expect(newestActiveOrchestrator([newUpdate, sameTimesHigherID])).toBe(sameTimesHigherID);
	});
});

describe("projectRowActive", () => {
	function workspaceWith(sessions: WorkspaceSession[]): WorkspaceSummary {
		return { id: "skills", name: "skills", path: "/tmp/skills", sessions };
	}

	it("highlights the project whose board is open (no session in the route)", () => {
		expect(projectRowActive(workspaceWith([]), "skills", undefined)).toBe(true);
	});

	it("does not highlight a project that is not the open one", () => {
		const ws = workspaceWith([]);
		expect(projectRowActive(ws, "other", undefined)).toBe(false);
		expect(projectRowActive(ws, undefined, undefined)).toBe(false);
	});

	it("highlights the owning project when its orchestrator session is open", () => {
		// The orchestrator has no worker row of its own, so the project row must
		// carry the active highlight — exactly like the board does.
		const orch = sessionWith({ id: "skills-orchestrator", kind: "orchestrator", status: "working" });
		expect(projectRowActive(workspaceWith([orch]), "skills", "skills-orchestrator")).toBe(true);
	});

	it("recognises an orchestrator via the id suffix when kind is absent", () => {
		const orch = sessionWith({ id: "skills-orchestrator", status: "working" });
		expect(projectRowActive(workspaceWith([orch]), "skills", "skills-orchestrator")).toBe(true);
	});

	it("leaves the project row inactive when a worker session is open", () => {
		// A worker session highlights its own child row instead.
		const worker = sessionWith({ id: "skills-3", kind: "worker", status: "working" });
		expect(projectRowActive(workspaceWith([worker]), "skills", "skills-3")).toBe(false);
	});

	it("does not highlight when the open session belongs to a different project", () => {
		const orch = sessionWith({ id: "skills-orchestrator", kind: "orchestrator", status: "working" });
		expect(projectRowActive(workspaceWith([orch]), "other", "other-orchestrator")).toBe(false);
	});
});

describe("sessionNeedsAttention", () => {
	it.each(["needs_input", "no_signal", "changes_requested", "review_pending", "ci_failed"] as const)(
		"is true for %s",
		(status) => {
			expect(sessionNeedsAttention(sessionWith({ status }))).toBe(true);
		},
	);

	it("treats no_signal as needing attention", () => {
		expect(sessionNeedsAttention(sessionWith({ status: "no_signal" }))).toBe(true);
	});

	it("is false for statuses that don't need the user", () => {
		expect(sessionNeedsAttention(sessionWith({ status: "working" }))).toBe(false);
		expect(sessionNeedsAttention(sessionWith({ status: "mergeable" }))).toBe(false);
	});
});

describe("orchestratorHealth", () => {
	it("reports restart_needed when the configured orchestrator agent differs from the newest active orchestrator", () => {
		const older = sessionWith({
			id: "skills-1",
			kind: "orchestrator",
			provider: "codex",
			status: "working",
			createdAt: "2026-01-01T00:00:00Z",
			updatedAt: "2026-01-01T00:00:00Z",
		});
		const newest = sessionWith({
			id: "skills-2",
			kind: "orchestrator",
			provider: "claude-code",
			status: "working",
			createdAt: "2026-01-02T00:00:00Z",
			updatedAt: "2026-01-02T00:00:00Z",
		});

		expect(
			orchestratorHealth({
				id: "skills",
				name: "skills",
				path: "/tmp/skills",
				orchestratorAgent: "codex",
				sessions: [older, newest],
			}),
		).toEqual({
			state: "duplicates",
			message:
				"Multiple orchestrators are active. The newest one is used; stale ones will be cleaned up on daemon reconcile.",
		});

		expect(
			orchestratorHealth({
				id: "skills",
				name: "skills",
				path: "/tmp/skills",
				orchestratorAgent: "codex",
				sessions: [newest],
			}).state,
		).toBe("restart_needed");
	});
});

describe("workerStatusPulses", () => {
	it("pulses only for working and needs_you", () => {
		expect(workerStatusPulses("working")).toBe(true);
		expect(workerStatusPulses("needs_you")).toBe(true);
		expect(workerStatusPulses("mergeable")).toBe(false);
		expect(workerStatusPulses("no_signal")).toBe(false);
		expect(workerStatusPulses("done")).toBe(false);
		expect(workerStatusPulses("unknown")).toBe(false);
	});
});

describe("toAgentProvider", () => {
	it("passes through a known provider", () => {
		expect(toAgentProvider("opencode")).toBe("opencode");
	});

	it("defaults unknown and undefined providers to codex", () => {
		expect(toAgentProvider("totally-unknown")).toBe("codex");
		expect(toAgentProvider(undefined)).toBe("codex");
	});
});

describe("PR helpers", () => {
	const session = sessionWith({
		prs: [
			pr({ number: 41, state: "open" }),
			pr({ number: 42, state: "draft" }),
			pr({ number: 40, state: "merged" }),
			pr({ number: 39, state: "closed" }),
		],
	});

	it("sortedPRs orders open, draft, merged, closed then by number", () => {
		expect(sortedPRs(session).map((p) => p.number)).toEqual([41, 42, 40, 39]);
	});

	it("openPRs returns open and draft only", () => {
		expect(
			openPRs(session)
				.map((p) => p.number)
				.sort(),
		).toEqual([41, 42]);
	});

	it("mergedPRCount counts merged PRs", () => {
		expect(mergedPRCount(session)).toBe(1);
	});

	it("primaryPR is the highest-priority PR (open before merged)", () => {
		expect(primaryPR(session)?.number).toBe(41);
	});

	it("primaryPR is undefined when there are no PRs", () => {
		expect(primaryPR(sessionWith({ prs: [] }))).toBeUndefined();
	});
});

describe("isMergeSuspended / mergedSuspendPRNumber", () => {
	it("is true for a suspended worker whose PRs are all terminal with ≥1 merged", () => {
		const s = sessionWith({ isSuspended: true, prs: [pr({ number: 7, state: "merged" })] });
		expect(isMergeSuspended(s)).toBe(true);
		expect(mergedSuspendPRNumber(s)).toBe(7);
	});

	it("names the highest merged PR number when several merged", () => {
		const s = sessionWith({
			isSuspended: true,
			prs: [pr({ number: 3, state: "merged" }), pr({ number: 9, state: "merged" }), pr({ number: 5, state: "closed" })],
		});
		expect(isMergeSuspended(s)).toBe(true);
		expect(mergedSuspendPRNumber(s)).toBe(9);
	});

	it("is false when NOT suspended (a plain merged session archives to Done)", () => {
		expect(isMergeSuspended(sessionWith({ isSuspended: false, prs: [pr({ number: 7, state: "merged" })] }))).toBe(
			false,
		);
	});

	it("is false when an open PR remains (still live) — this is idle-suspend territory", () => {
		const s = sessionWith({
			isSuspended: true,
			prs: [pr({ number: 7, state: "merged" }), pr({ number: 8, state: "open" })],
		});
		expect(isMergeSuspended(s)).toBe(false);
	});

	it("is false when suspended but nothing merged (all closed, or no PRs) — idle-suspend", () => {
		expect(isMergeSuspended(sessionWith({ isSuspended: true, prs: [pr({ number: 7, state: "closed" })] }))).toBe(false);
		expect(isMergeSuspended(sessionWith({ isSuspended: true, prs: [] }))).toBe(false);
		expect(
			mergedSuspendPRNumber(sessionWith({ isSuspended: true, prs: [pr({ number: 7, state: "closed" })] })),
		).toBeUndefined();
	});
});

describe("attentionZone", () => {
	const cases: Array<[SessionStatus, AttentionZone]> = [
		["mergeable", "merge"],
		["approved", "merge"],
		["needs_input", "action"],
		["no_signal", "action"],
		["ci_failed", "action"],
		["changes_requested", "action"],
		["review_pending", "pending"],
		["pr_open", "pending"],
		["draft", "pending"],
		["unknown", "pending"],
		["working", "working"],
		["idle", "working"],
		["merged", "done"],
		["terminated", "done"],
	];

	it.each(cases)("buckets %s into the %s zone", (status, zone) => {
		expect(attentionZone(sessionWith({ status }))).toBe(zone);
	});

	it("prioritizes merge as the highest-ROI zone", () => {
		// merge is checked before action/pending so an approved PR always surfaces.
		expect(attentionZone(sessionWith({ status: "approved" }))).toBe("merge");
	});
});

describe("toStatusReason", () => {
	it("passes through known reasons, maps unknown to 'unknown', and undefined to undefined", () => {
		expect(toStatusReason("active_stale")).toBe("active_stale");
		expect(toStatusReason("waiting_input")).toBe("waiting_input");
		expect(toStatusReason("bogus")).toBe("unknown");
		expect(toStatusReason(undefined)).toBeUndefined();
	});
});

describe("statusReasonLabel", () => {
	it("has a non-empty label for every real reason code", () => {
		for (const r of [
			"working",
			"waiting_input",
			"active_stale",
			"idle_aged",
			"idle",
			"no_signal",
			"pr_pipeline",
			"terminated",
			"merged",
		] as const) {
			expect(statusReasonLabel[r].length).toBeGreaterThan(0);
		}
	});
});

describe("formatNextTransition", () => {
	const now = Date.parse("2026-01-01T00:00:00Z");

	it("formats a pending flip with target and duration", () => {
		expect(
			formatNextTransition({ nextTransitionAt: "2026-01-01T00:04:00Z", nextTransitionTo: "needs_input" }, now),
		).toBe("→ Needs input in 4m");
		expect(formatNextTransition({ nextTransitionAt: "2026-01-01T00:00:30Z", nextTransitionTo: "no_signal" }, now)).toBe(
			"→ No signal in 30s",
		);
	});

	it("is empty when already due, missing, or targeting a non-countdown status", () => {
		expect(
			formatNextTransition({ nextTransitionAt: "2025-12-31T23:59:00Z", nextTransitionTo: "needs_input" }, now),
		).toBe("");
		expect(formatNextTransition({}, now)).toBe("");
		expect(formatNextTransition({ nextTransitionAt: "2026-01-01T00:04:00Z", nextTransitionTo: "working" }, now)).toBe(
			"",
		);
	});
});
