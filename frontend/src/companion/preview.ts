import type { SessionStatus } from "../renderer/types/workspace";
import type { BubbleDecay, BubbleTone } from "./Bubble";
import { composeCast, HATS, PALETTES, type CastMember } from "./cast";
import { ALL_COMPANION_STATUSES } from "./scene";

// The data behind the Settings gallery: every state a Proc can be in, with a
// plain-English line saying what it means.
//
// This exists because the overlay is the one surface a user cannot browse. It sits
// along the bottom of the screen showing whatever the sessions happen to be doing,
// so most states are never seen, and the ones that are get no caption. A gallery is
// how someone learns that a bed means idle and a `?` sign means it wants them.
//
// Characters are dealt round the states so the gallery doubles as a cast list, and
// the whole thing is a pure function so it cannot reshuffle between renders.

export type PreviewBubble = { text: string; tone?: BubbleTone; decay?: BubbleDecay };

export type PreviewEntry = {
	status: SessionStatus;
	cast: CastMember;
	/** What this state means, in words. A status id is not an explanation. */
	label: string;
	/** Only the states that would really have one. */
	bubble?: PreviewBubble;
};

// Wording is the user's-eye view, not the enum's. "no_signal" is a fact about our
// connection; "we have lost contact" is what it means to the person reading it.
export const STATUS_LABELS: Record<SessionStatus, string> = {
	todo: "Queued up, not started yet",
	working: "At the desk, running",
	pr_open: "Pull request is up",
	draft: "Draft — nothing written yet",
	ci_failed: "CI failed",
	review_pending: "Waiting on a reviewer",
	changes_requested: "A reviewer asked for changes",
	approved: "Approved",
	mergeable: "Ready for you to merge",
	merged: "Merged — done",
	needs_input: "Waiting for you to answer",
	no_signal: "We have lost contact",
	idle: "Resting between jobs",
	terminated: "Ended without merging",
	unknown: "Status unclear",
};

// Bubbles appear ONLY where a live session would really produce one. A Proc whose
// session has merged, ended or gone silent says nothing — showing a bubble on those
// in the gallery would teach the opposite of how the feature behaves.
const BUBBLES: Partial<Record<SessionStatus, PreviewBubble>> = {
	working: { text: "Running the test suite", decay: "fresh" },
	ci_failed: { text: "The build step failed", decay: "fresh" },
	needs_input: { text: "Waiting for you", tone: "alert", decay: "fresh" },
	review_pending: { text: "Opened the pull request", decay: "fading" },
	idle: { text: "Quiet for 12 minutes", decay: "settled" },
};

/** Every state, with a character, a caption and — where it is honest — a bubble. */
export function previewRoster(): PreviewEntry[] {
	return ALL_COMPANION_STATUSES.map((status, index) => ({
		status,
		// Both axes stepped, at rates coprime with their lengths, so a gallery of
		// fifteen states shows all six colours AND all six hats rather than six pairs.
		cast: composeCast(
			PALETTES[index % PALETTES.length],
			HATS[(index + Math.floor(index / PALETTES.length)) % HATS.length],
		),
		label: STATUS_LABELS[status],
		bubble: BUBBLES[status],
	}));
}

/**
 * One claim walked through its whole life, for the gallery's "what the bubble does
 * over time" row. Shown as a ladder because decay is the least obvious and most
 * important thing about the bubble: it is what stops a Proc claiming, ten minutes
 * later, to still be doing something it finished.
 */
export const PREVIEW_BUBBLES: Array<PreviewBubble & { caption: string }> = [
	{ text: "Running the test suite", decay: "fresh", caption: "Fresh — just happened" },
	{ text: "Running the test suite", decay: "fading", caption: "Older — a weaker claim looks weaker" },
	{ text: "Running the test suite", decay: "settled", caption: "Stale — collapses to what is still true" },
];
