// Where two bubbles collide, one goes ABOVE the other.
//
// This replaces three attempts at the same problem — flipping which way a card
// opens, reordering which Proc paints in front, and suppressing one of the two
// cards outright. All three were the same mistake: treating a collision between
// two things that both need to be read as a question of which one wins. The band
// is horizontal and crowded and the sky above it is empty, so the answer is to
// use the sky.
//
// Deterministic and stable by construction: bubbles are placed left to right,
// each takes the LOWEST lane it fits in, and the span used is the widest a card
// can ever be rather than the width of the words currently in it — so a sentence
// growing a line cannot make the whole stack jump about.

export type BubbleSpan = {
	id: string;
	/** Left edge of the widest card this Proc could show, in screen px. */
	left: number;
	/** Right edge of the same. */
	right: number;
};

/** How many bubbles may stack before the rest share the top lane. */
export const MAX_BUBBLE_LANES = 3;

/**
 * Lane per bubble: 0 sits at its Proc's head, 1 is one card-height above it, and
 * so on. Bubbles that do not overlap anything all stay at 0.
 */
export function assignBubbleLanes(spans: BubbleSpan[]): Map<string, number> {
	// Left to right, ties by id, so the same set of Procs always produces the same
	// arrangement whatever order they arrived in.
	const ordered = [...spans].sort((a, b) => a.left - b.left || a.id.localeCompare(b.id));
	const lanes: BubbleSpan[][] = [];
	const placed = new Map<string, number>();

	for (const span of ordered) {
		let lane = lanes.findIndex((occupants) => occupants.every((other) => other.right <= span.left));
		if (lane === -1) {
			// A card wider than every free lane goes up one — until the top lane, which
			// takes the overflow. Stacking without limit would walk off the display,
			// and a card off the display is worse than two that touch.
			lane = Math.min(lanes.length, MAX_BUBBLE_LANES - 1);
		}
		if (!lanes[lane]) lanes[lane] = [];
		lanes[lane].push(span);
		placed.set(span.id, lane);
	}

	return placed;
}
