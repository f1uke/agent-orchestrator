import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import type { SmokeProgress } from "./smoke-test";
import { prTitleLabel } from "./pr-display";
import type { SessionActivityState, SessionStatus } from "../types/workspace";

/**
 * The Summary-tab "readiness / gating" strip derivation.
 *
 * A session's work travels the AO merge pipeline — Work → Smoke → PR → CI →
 * Review → Merge — and the strip answers "how far along, and is it ready?" at a
 * glance. Everything here is a PURE function of data already on the wire (PR
 * summaries + the smoke rollup + session activity); it invents no new backend
 * facts. The verdict and the gate row are derived from the SAME inputs so the
 * headline can never contradict the gates it summarizes.
 *
 * The gates are independent facts, not a strict sequence: a later gate can be
 * green while an earlier one is red, so the strip surfaces ALL blockers at once
 * rather than hiding them behind a single progress bar.
 */

/** Per-gate state: pass (green) · wait (amber) · block (red) · idle (grey / N/A). */
export type ReadinessTone = "pass" | "wait" | "block" | "idle";

/** Verdict hue → the sanctioned board lane palette. */
export type ReadinessHue = "working" | "review" | "needs" | "merge" | "todo";

export type ReadinessGateKey = "work" | "smoke" | "pr" | "ci" | "review" | "merge";

export type ReadinessGate = {
	key: ReadinessGateKey;
	label: string;
	/** One-word live state, e.g. "passing", "changes", "not run". */
	state: string;
	tone: ReadinessTone;
};

export type ReadinessVerdict = {
	hue: ReadinessHue;
	/** Headline word the user reads first, e.g. "Changes Requested". */
	word: string;
	/** One-line "why / what to do next". */
	caption: string;
	/** Pulse the dot for act-now states (Working, Ready to Merge). */
	pulse?: boolean;
};

export type Readiness = {
	verdict: ReadinessVerdict;
	gates: ReadinessGate[];
	/** The gate the verdict is about (first block, else first wait) — gets the ring. */
	currentKey?: ReadinessGateKey;
	/** Right-aligned context, e.g. "MR !3028 · open". Empty when no PR yet. */
	contextLabel: string;
};

type SessionFacts = {
	activity?: { state?: SessionActivityState } | null;
	status?: SessionStatus;
};

const gate = (key: ReadinessGateKey, label: string, tone: ReadinessTone, state: string): ReadinessGate => ({
	key,
	label,
	tone,
	state,
});

/** The PR the readiness verdict is about: the most actionable one. `prs` arrives
 * sorted actionable-first (open → draft → merged → closed), so the head wins. */
function primaryPR(prs: SessionPRSummary[]): SessionPRSummary | undefined {
	return prs[0];
}

function workGate(session: SessionFacts, hasPR: boolean, merged: boolean): ReadinessGate {
	if (merged || hasPR) return gate("work", "Work", "pass", "done");
	if (session.activity?.state === "active") return gate("work", "Work", "wait", "working");
	return gate("work", "Work", "pass", "done");
}

function prGate(pr: SessionPRSummary | undefined): ReadinessGate {
	if (!pr) return gate("pr", "PR", "idle", "none");
	switch (pr.state) {
		case "draft":
			return gate("pr", "PR", "wait", "draft");
		case "merged":
			return gate("pr", "PR", "pass", "merged");
		case "closed":
			return gate("pr", "PR", "idle", "closed");
		default:
			return gate("pr", "PR", "pass", "open");
	}
}

function ciGate(pr: SessionPRSummary | undefined): ReadinessGate {
	if (!pr) return gate("ci", "CI", "idle", "—");
	switch (pr.ci.state) {
		case "passing":
			return gate("ci", "CI", "pass", "passing");
		case "failing":
			return gate("ci", "CI", "block", "failing");
		case "pending":
			return gate("ci", "CI", "wait", "running");
		default:
			return gate("ci", "CI", "wait", "checking");
	}
}

/** Review = approvals + changes-requested collapsed into one gate. Approvals are
 * decision-derived (no numeric count is on the wire); unresolved human comment
 * threads soften an otherwise-quiet review to "comments". */
function reviewGate(pr: SessionPRSummary | undefined): ReadinessGate {
	if (!pr) return gate("review", "Review", "idle", "—");
	if (pr.state === "merged" || pr.state === "closed") return gate("review", "Review", "pass", "done");
	switch (pr.review.decision) {
		case "approved":
			return gate("review", "Review", "pass", "approved");
		case "changes_requested":
			return gate("review", "Review", "block", "changes");
		case "review_required":
			return gate("review", "Review", "wait", "required");
		default:
			return pr.review.hasUnresolvedHumanComments
				? gate("review", "Review", "wait", "comments")
				: gate("review", "Review", "idle", "awaiting");
	}
}

function smokeGate(smoke: SmokeProgress): ReadinessGate {
	if (smoke.fail > 0) return gate("smoke", "Smoke", "block", "failed");
	if (smoke.total === 0) return gate("smoke", "Smoke", "idle", "not run");
	if (smoke.pending > 0) return gate("smoke", "Smoke", "wait", "running");
	return gate("smoke", "Smoke", "pass", "passed");
}

function mergeGate(pr: SessionPRSummary | undefined): ReadinessGate {
	if (!pr) return gate("merge", "Merge", "idle", "—");
	if (pr.state === "merged") return gate("merge", "Merge", "pass", "merged");
	if (pr.state === "closed") return gate("merge", "Merge", "idle", "closed");
	switch (pr.mergeability.state) {
		case "mergeable":
			return gate("merge", "Merge", "pass", "clean");
		case "conflicting":
			return gate("merge", "Merge", "block", "conflict");
		case "blocked":
		case "unstable":
			return gate("merge", "Merge", "wait", "blocked");
		default:
			return gate("merge", "Merge", "wait", "checking");
	}
}

function contextLabel(pr: SessionPRSummary | undefined): string {
	if (!pr) return "";
	return `${prTitleLabel(pr.provider, pr.number)} · ${pr.state}`;
}

/** The gate the verdict points at: the first blocker, else the first waiter. */
function currentGate(gates: ReadinessGate[]): ReadinessGate | undefined {
	return gates.find((g) => g.tone === "block") ?? gates.find((g) => g.tone === "wait");
}

function deriveVerdict(
	session: SessionFacts,
	pr: SessionPRSummary | undefined,
	gates: Record<ReadinessGateKey, ReadinessGate>,
): ReadinessVerdict {
	const merged = pr?.state === "merged" || session.status === "merged";

	if (merged) return { hue: "merge", word: "Merged", caption: "Work is merged." };
	if (pr?.state === "closed")
		return { hue: "todo", word: "Closed", caption: "This pull request was closed without merging." };

	// No PR yet — the merge pipeline isn't active, so pipeline blockers (a failed
	// smoke check, etc.) never headline over the fact that work is still underway.
	const hasPR = pr?.state === "open" || pr?.state === "draft";
	if (!hasPR)
		return {
			hue: "working",
			word: "Working",
			caption: "Agent is still working — no pull request yet.",
			pulse: session.activity?.state === "active",
		};

	// Blockers (needs-you) — ordered by how much a human is on the hook.
	if (gates.review.tone === "block")
		return { hue: "needs", word: "Changes Requested", caption: "Resolve the review feedback before this can merge." };
	if (gates.ci.tone === "block")
		return { hue: "needs", word: "CI Failing", caption: "One or more checks are failing." };
	if (gates.merge.tone === "block")
		return { hue: "needs", word: "Merge Conflict", caption: "Resolve conflicts with the base branch." };
	if (gates.smoke.tone === "block")
		return { hue: "needs", word: "Smoke Failed", caption: "A smoke check didn’t pass." };

	// Ready — every applicable gate is green. A smoke checklist that was never
	// authored ("not run", idle) does not block; an authored-but-pending one does.
	const ready =
		pr!.state === "open" &&
		gates.ci.tone === "pass" &&
		(gates.review.tone === "pass" || gates.review.tone === "idle") &&
		(gates.smoke.tone === "pass" || gates.smoke.tone === "idle") &&
		gates.merge.tone === "pass";
	if (ready) return { hue: "merge", word: "Ready to Merge", caption: "All gates pass — you can merge.", pulse: true };

	// In-flight — surface the earliest gate still in motion.
	if (pr!.state === "draft") return { hue: "todo", word: "Draft", caption: "Mark the draft ready for review." };
	if (gates.ci.tone === "wait") return { hue: "review", word: "Waiting on CI", caption: "Checks are running." };
	if (gates.review.tone === "wait" || gates.review.tone === "idle")
		return { hue: "review", word: "In Review", caption: "Waiting on review approval." };
	if (gates.smoke.tone === "wait")
		return { hue: "review", word: "Waiting on Smoke", caption: "Play the smoke checks to confirm." };
	return { hue: "review", word: "In Review", caption: "Waiting on the merge pipeline." };
}

export function deriveReadiness(session: SessionFacts, prs: SessionPRSummary[], smoke: SmokeProgress): Readiness {
	const pr = primaryPR(prs);
	const hasPR = pr?.state === "open" || pr?.state === "draft" || pr?.state === "merged";
	const merged = pr?.state === "merged";

	// Order mirrors the real flow: Work → Smoke → PR → CI → Review → Merge. Smoke
	// is authored before the PR is opened, so it sits right after Work. This array
	// order is also what currentGate() walks to pick the ring (first block, else
	// first wait), so a pre-PR session with an authored-but-pending smoke check
	// lights Smoke — the earliest live gate — rather than an idle downstream one.
	const list: ReadinessGate[] = [
		workGate(session, hasPR, merged),
		smokeGate(smoke),
		prGate(pr),
		ciGate(pr),
		reviewGate(pr),
		mergeGate(pr),
	];
	const byKey = Object.fromEntries(list.map((g) => [g.key, g])) as Record<ReadinessGateKey, ReadinessGate>;

	const verdict = deriveVerdict(session, pr, byKey);
	return {
		verdict,
		gates: list,
		currentKey: currentGate(list)?.key,
		contextLabel: contextLabel(pr),
	};
}
