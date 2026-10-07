// Pure helpers for the Summary tab's "Machine runs" strip: a crew member's
// bracketed build/test runs and what the tree-write detector concluded about
// each one.
//
// # Why a run has FOUR states and not two
//
// Two members of a task share one worktree, so a build can read a tree the other
// member is halfway through writing. The result is not lost - it is
// untrustworthy, and the dangerous part is that it looks fine. So AO counts
// writes to the worktree across the run, and a run whose tree moved is
// DISCARDED: neither a pass (that is the laundering the whole mechanism exists
// to stop) nor a fail (that would blame the code). A run nothing was watching is
// UNCERTIFIED, for the same reason in the other direction - if a detector that
// misses is bad, an absent one has to be visible too.
//
// The machine's lane is MONOCHROME: only a real failure carries a verdict hue.
// A discarded or uncertified run therefore carries the neutral qa ink.

import type { components } from "../../api/schema";
import { tint } from "./comment-inbox";

/** The strip's palette, by role. Each value is a themed token (see `styles.css`),
 * so the strip follows the app between light and dark. Surfaces, borders and the
 * text ramp are the --inbox-* tokens the Comments tab uses. */
export const PALETTE = {
	cardBg: "var(--crew-run-card-bg)",
	pillBg: "var(--inbox-pill-bg)",
	borderCard: "var(--inbox-border-card)",
	borderPill: "var(--inbox-border-pill)",
	borderExpand: "var(--crew-run-divider-expand)",
	text: "var(--inbox-text)",
	secondary: "var(--inbox-secondary)",
	secondary2: "var(--inbox-secondary-2)",
	muted2: "var(--inbox-muted-2)",
	refChip: "var(--viewer-chrome-fg)",
	fail: "var(--crew-run-fail)",
	failFg: "var(--crew-run-fail-fg)",
	/** A flat, neutral block. Deliberately not accent-tinted (that is the app's
	 * own voice) and never a verdict hue. */
	qaBg: "var(--crew-run-qa-bg)",
	qaBorder: "var(--crew-run-qa-border)",
	qaFg: "var(--crew-run-qa-fg)",
} as const;

export type CrewRun = components["schemas"]["CrewRun"];

export type CrewRunState = "running" | "passed" | "failed" | "discarded" | "uncertified" | "finished";

/** The one word a run reads as. Mirrors domain.CrewRun.State in the backend. */
export function crewRunState(run: CrewRun): CrewRunState {
	if (!run.endedAt) return "running";
	if (run.outcome === "discarded") return "discarded";
	if (run.outcome === "uncertified") return "uncertified";
	if (run.result === "pass") return "passed";
	if (run.result === "fail") return "failed";
	return "finished";
}

export type CrewRunMeta = {
	/** Chip text. */
	label: string;
	/** One line saying what the state MEANS, so nobody has to infer it. */
	caption: string;
	color: string;
	pillBg: string;
	pillBorder: string;
};

export const CREW_RUN_META: Record<CrewRunState, CrewRunMeta> = {
	running: {
		label: "Running",
		caption: "This member is running something right now.",
		color: PALETTE.qaFg,
		pillBg: PALETTE.qaBg,
		pillBorder: PALETTE.qaBorder,
	},
	passed: {
		label: "Passed",
		caption: "The tree did not move, so what this run saw is what the tree is.",
		color: PALETTE.qaFg,
		pillBg: PALETTE.qaBg,
		pillBorder: PALETTE.qaBorder,
	},
	failed: {
		label: "Failed",
		caption: "The tree did not move, so this failure is real.",
		color: PALETTE.failFg,
		pillBg: tint(PALETTE.fail, 14),
		pillBorder: tint(PALETTE.fail, 40),
	},
	discarded: {
		label: "Discarded",
		caption: "The tree changed under this run, so its result was thrown away - not passed, not failed.",
		color: PALETTE.qaFg,
		pillBg: PALETTE.qaBg,
		pillBorder: PALETTE.qaBorder,
	},
	uncertified: {
		label: "Uncertified",
		caption: "Nothing was watching the tree, so this result cannot be vouched for.",
		color: PALETTE.qaFg,
		pillBg: PALETTE.qaBg,
		pillBorder: PALETTE.qaBorder,
	},
	finished: {
		label: "Finished",
		caption: "The tree did not move. The run recorded no pass or fail of its own.",
		color: PALETTE.qaFg,
		pillBg: PALETTE.qaBg,
		pillBorder: PALETTE.qaBorder,
	},
};

export function crewRunMeta(run: CrewRun): CrewRunMeta {
	return CREW_RUN_META[crewRunState(run)];
}

/** How long the run took, or has been going. Short forms only. */
export function crewRunDuration(run: CrewRun, now: number): string {
	const started = Date.parse(run.startedAt);
	if (Number.isNaN(started)) return "";
	const ended = run.endedAt ? Date.parse(run.endedAt) : now;
	if (Number.isNaN(ended)) return "";
	const secs = Math.max(0, Math.round((ended - started) / 1000));
	if (secs < 60) return `${secs}s`;
	const mins = Math.floor(secs / 60);
	if (mins < 60) return `${mins}m ${secs % 60}s`;
	return `${Math.floor(mins / 60)}h ${mins % 60}m`;
}

/**
 * The CURRENT streak of discarded runs in ONE member's runs, counted from the
 * newest backwards. Mirrors the daemon's ConsecutiveCrewRunDiscards, which is
 * what the card's lane is derived from, so the strip and the board never
 * disagree.
 *
 * Only a TRUSTED run ends the streak. Two states are SKIPPED rather than
 * counted or treated as a clear:
 *
 * - a run still open, because it has not been judged yet, and reading "not yet
 *   decided" as "fine" would make the escalation vanish the instant the member
 *   started its next attempt - exactly when a person needs to see it;
 * - an UNCERTIFIED run, because nothing watched the tree, so it is not evidence
 *   that this member got a quiet window. Letting it clear the streak would mean
 *   a daemon restart quietly cancelled the escalation.
 */
export function discardStreak(runs: CrewRun[]): number {
	let streak = 0;
	for (const run of runs) {
		const state = crewRunState(run);
		if (state === "running" || state === "uncertified") continue;
		if (state !== "discarded") break;
		streak += 1;
	}
	return streak;
}

/** How many discards are retried automatically before a human has to decide.
 * The backend's domain.CappedRepeat; kept here so the copy can name it. */
export const CREW_RUN_MAX_ATTEMPTS = 3;

/**
 * The member whose streak has spent the automatic retry and parked the task at
 * NEEDS YOU, if one has. A crew's strip mixes both members' runs, newest first,
 * but a streak is one member's: dev's quiet runs between qa's discards are no
 * evidence that qa ever got a quiet tree.
 */
export function crewRunEscalation(runs: CrewRun[]): { streak: number; role?: CrewRun["role"] } | undefined {
	const members = new Map<string, CrewRun[]>();
	for (const run of runs) members.set(run.sessionId, [...(members.get(run.sessionId) ?? []), run]);
	for (const memberRuns of members.values()) {
		const streak = discardStreak(memberRuns);
		if (streak >= CREW_RUN_MAX_ATTEMPTS) return { streak, role: memberRuns[0].role };
	}
	return undefined;
}

/** The label line for one run: its kind, and the command if the member named one. */
export function crewRunTitle(run: CrewRun): string {
	const kind = run.kind ? run.kind[0].toUpperCase() + run.kind.slice(1) : "Run";
	return run.label ? `${kind} · ${run.label}` : kind;
}
