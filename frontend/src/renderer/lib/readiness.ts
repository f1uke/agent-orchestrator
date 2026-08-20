import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import type { SmokeProgress } from "./smoke-test";
import { approvalLabel, approvalProgress, prTitleLabel } from "./pr-display";
import type { SessionActivityState, SessionStatus, SessionTermination } from "../types/workspace";

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
	/** How the session ended, when it has. Read only to caption a stopped
	 * session with who ended it. */
	termination?: SessionTermination | null;
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
	// Ended with no pull request to show for it. "done" would assert that the
	// work landed, which is precisely what did not happen — and it is the reading
	// that let a worker stop mid-task without anyone noticing.
	if (session.status === "terminated") return gate("work", "Work", "block", "stopped");
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

/** The Review gate's state when nobody has looked at the pull request yet. Named
 * because the verdict keys off it to say so out loud, rather than folding it in
 * with a review that is genuinely under way. */
const REVIEW_AWAITING = "awaiting";

/** Review = approvals + changes-requested collapsed into one gate. When a project
 * or SCM approval rule applies, the node shows A/T progress and flips green at the
 * threshold (amber while short — the live gate you're waiting on). Changes
 * requested always wins. Absent a rule, it falls back to the decision label;
 * unresolved human comment threads soften an otherwise-quiet review to
 * "comments".
 *
 * AO's own reviewer is folded in STRICTLY ADDITIVELY. It can block a gate the
 * provider left quiet, and it can answer the "nobody has looked" case — but it
 * never satisfies a real approval rule (a numeric threshold or an explicit
 * `review_required` is about human approvers on the forge, and AO is not one of
 * them, and those cases keep their own label — the state text truncates past
 * ~12 characters, so there is no room to name both). An AO verdict is only ever reported at the PR's current head, so a green
 * "AO ✓" cannot be stale. */
function reviewGate(pr: SessionPRSummary | undefined): ReadinessGate {
	if (!pr) return gate("review", "Review", "idle", "—");
	if (pr.state === "merged" || pr.state === "closed") return gate("review", "Review", "pass", "done");
	// Changes requested is the priority signal; approval progress rides underneath
	// it and never turns a blocked review neutral.
	if (pr.review.decision === "changes_requested") return gate("review", "Review", "block", "changes");
	// AO requesting changes blocks for the same reason a human does: there is
	// feedback at this exact head that nobody has addressed.
	if (pr.aoReview?.verdict === "changes_requested") return gate("review", "Review", "block", "AO: changes");

	const progress = approvalProgress(pr.review);
	if (progress?.required != null) {
		// A known threshold makes the meter authoritative: green when met, amber
		// (the live gate) while short. AO does not count toward it.
		return gate("review", "Review", progress.met ? "pass" : "wait", approvalLabel(progress));
	}
	// Count-only (SCM rule, unknown threshold) or no rule: keep the decision label,
	// but surface the observed count when we have one.
	const count = progress ? approvalLabel(progress) : null;
	const aoApproved = pr.aoReview?.verdict === "approved";
	switch (pr.review.decision) {
		case "approved":
			return gate("review", "Review", "pass", count ?? "approved");
		case "review_required":
			// The forge is still waiting on a human, and that is the whole state: an
			// AO approval does not discharge the requirement, and the gate's state
			// label truncates past ~12 characters, so naming both here would produce
			// "AO ✓, huma…" — worse than saying the one thing that is blocking. AO's
			// verdict stays visible on the Reviews tab and on the PR payload.
			return gate("review", "Review", "wait", count ?? "required");
		default:
			if (count) return gate("review", "Review", "wait", count);
			// AO reviewed this head and approved it. That is not "nobody has looked" —
			// which is the whole reason REVIEW_AWAITING exists — so say who looked.
			if (aoApproved && !pr.review.hasUnresolvedHumanComments) return gate("review", "Review", "pass", "AO approved");
			// Nobody has reviewed it yet. That is a review still owed, not an
			// inapplicable gate: `idle` is the tone for "there was nothing to look
			// at" (no PR, closed PR), and reading it here is what let an unreviewed
			// PR headline as fully green.
			return pr.review.hasUnresolvedHumanComments
				? gate("review", "Review", "wait", "comments")
				: gate("review", "Review", "wait", REVIEW_AWAITING);
	}
}

/**
 * The smoke gate is `pass` only when a PERSON has judged every active case.
 *
 * A case can also carry a machine's result, and that result may move this
 * label or make the tone STRICTER - it may never stand in for the human's
 * verdict. The two answer different questions, and every regression a person has
 * caught by hand (recording latency, dead drag-scroll, keystrokes never
 * arriving, a tab pausing when unfocused, control lost after a lease lapse)
 * lives in the gap between them; a machine pass opening the merge gate would let
 * a card read green with nobody having touched the app. So this is AND-more,
 * never OR-instead: the `pass` return below is reachable only from
 * `pending === 0`, which counts human verdicts alone.
 *
 * Retired cases are excluded upstream (see progressFor) - a checklist that has
 * legitimately shrunk must not hold the gate open forever.
 */
function smokeGate(smoke: SmokeProgress): ReadinessGate {
	if (smoke.fail > 0) return gate("smoke", "Smoke", "block", "failed");
	if (smoke.total === 0) return gate("smoke", "Smoke", "idle", "not run");
	if (smoke.pending > 0) {
		// A machine says the steps did not even run. That is real information and
		// it is counted only over cases nobody has judged, so a person's verdict
		// always overrules it rather than being shouted down by a stale run.
		if (smoke.agentFail > 0) return gate("smoke", "Smoke", "block", "qa failed");
		if (smoke.agentPass > 0) return gate("smoke", "Smoke", "wait", `qa ${smoke.agentPass}/${smoke.pending}`);
		return gate("smoke", "Smoke", "wait", "running");
	}
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

/** Who ended a session that stopped without opening a pull request. An ending
 * with no recorded source (a session that ended before AO kept an account) says
 * only that it stopped, rather than guessing at a culprit. */
function stoppedCaption(source: string | undefined): string {
	switch (source) {
		case "agent":
			return "The agent ended this session itself — no pull request was opened.";
		case "ao":
			return "AO tore this session down — no pull request was opened.";
		case "runtime_gone":
			return "This session's terminal disappeared — no pull request was opened.";
		default:
			return "This session ended without opening a pull request.";
	}
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
	if (!hasPR) {
		// ...unless the session has ENDED. It opened no PR and it is not coming
		// back, so "agent is still working" is not merely stale, it is the false
		// reassurance that lets a worker vanish unnoticed: a session that quietly
		// stopped reads exactly like one still thinking. Say it stopped, and say
		// who stopped it.
		if (session.status === "terminated")
			return { hue: "todo", word: "Stopped", caption: stoppedCaption(session.termination?.source) };
		return {
			hue: "working",
			word: "Working",
			caption: "Agent is still working — no pull request yet.",
			pulse: session.activity?.state === "active",
		};
	}

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
	// Review gets no such pass: a checklist nobody wrote means there was nothing to
	// check, while a review nobody gave means nobody has looked. On an open PR the
	// review gate is never idle anyway (idle survives only for "no PR" and
	// "closed"), so requiring `pass` is the whole claim "all gates pass" makes.
	const ready =
		pr!.state === "open" &&
		gates.ci.tone === "pass" &&
		gates.review.tone === "pass" &&
		(gates.smoke.tone === "pass" || gates.smoke.tone === "idle") &&
		gates.merge.tone === "pass";
	if (ready) return { hue: "merge", word: "Ready to Merge", caption: "All gates pass — you can merge.", pulse: true };

	// In-flight — surface the earliest gate still in motion.
	if (pr!.state === "draft") return { hue: "todo", word: "Draft", caption: "Mark the draft ready for review." };
	if (gates.ci.tone === "wait") return { hue: "review", word: "Waiting on CI", caption: "Checks are running." };
	// "In Review" says someone is on the hook — an approval short of its threshold,
	// a requested review, an open comment thread. When nobody has looked at all,
	// say that instead: same blue lane, same non-blocking posture, but the human
	// reads the difference between "a review is running" and "no one has started".
	// It stays a verdict, not a veto — a solo author with no reviewer can still
	// merge; the strip just stops claiming the review already happened.
	if (gates.review.tone === "wait" && gates.review.state === REVIEW_AWAITING)
		return { hue: "review", word: "Waiting on Review", caption: "No one has reviewed this yet. Merging is your call." };
	if (gates.review.tone === "wait") return { hue: "review", word: "In Review", caption: "Waiting on review approval." };
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
