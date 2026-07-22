// Where two bubbles collide, one goes ABOVE the other.
//
// This replaces three attempts at the same problem — flipping which way a card
// opens, reordering which Proc paints in front, and suppressing one of the two
// cards outright. All three were the same mistake: treating a collision between
// two things that both need to be read as a question of which one wins. The band
// is horizontal and crowded and the sky above it is empty, so the answer is to
// use the sky.
//
// It works off the cards' MEASURED sizes, not the widest and tallest a card could
// ever be. Sized by the maximum, a 145px card was treated as 200px and lifted
// clear of a neighbour it never touched, and every step up the stack was a
// three-line card tall whatever was actually in it. Both were visible as soon as
// anyone looked at a real band.

export type BubbleBox = {
	id: string;
	/** Left edge of the card as it is actually drawn, in screen px. */
	left: number;
	/** Right edge of the same. */
	right: number;
	/** Drawn height of the card. */
	height: number;
};

/** How many bubbles may stack before the rest share the top lane. */
export const MAX_BUBBLE_LANES = 3;
/** Air between a card and the one it is sitting on top of. */
export const BUBBLE_STACK_GAP = 6;

/**
 * How far above its Proc each bubble sits, in px. Zero for a bubble that is in
 * nobody's way, which on a quiet desktop is all of them.
 *
 * Deterministic and stable by construction: cards are placed left to right, ties
 * broken by id, and each takes the LOWEST lane it fits in.
 */
export function stackBubbles(boxes: BubbleBox[]): Map<string, number> {
	const ordered = [...boxes].sort((a, b) => a.left - b.left || a.id.localeCompare(b.id));
	const lanes: BubbleBox[][] = [];
	const lane = new Map<string, number>();

	for (const box of ordered) {
		let index = lanes.findIndex((occupants) => occupants.every((other) => other.right <= box.left));
		if (index === -1) {
			// Nothing free below: go up one — until the top lane, which takes the
			// overflow. Stacking without limit walks a card off the display, and a card
			// off the display is worse than two that touch.
			index = Math.min(lanes.length, MAX_BUBBLE_LANES - 1);
		}
		if (!lanes[index]) lanes[index] = [];
		lanes[index].push(box);
		lane.set(box.id, index);
	}

	// A lane sits clear of the TALLEST card in the lane below it, not of the tallest
	// card a bubble could ever be — three lines' worth of air above a one-line card
	// reads as two unrelated things rather than a stack.
	const offsets: number[] = [];
	let running = 0;
	for (let index = 0; index < lanes.length; index++) {
		offsets[index] = running;
		const tallest = Math.max(0, ...(lanes[index] ?? []).map((box) => box.height));
		running += tallest + BUBBLE_STACK_GAP;
	}

	return new Map([...lane].map(([id, index]) => [id, offsets[index] ?? 0]));
}
