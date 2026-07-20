/**
 * Which stacked section is the reader currently looking at?
 *
 * Split out as a pure function on purpose: jsdom gives every element a zero
 * layout box, so the scroll listener that feeds this can only be verified by eye
 * in the real app — but the DECISION it encodes is exactly the part that gets
 * off-by-one, and this much is testable.
 *
 * @param tops    each section's top edge, in the same coordinate space as `anchor`
 *                (viewport coordinates from `getBoundingClientRect`)
 * @param anchor  the line a section must cross to count as "being read", usually
 *                the scroll container's top plus the sticky-header height
 * @returns index of the active section, or -1 when there are none
 */
export function activeIndexFromTops(tops: readonly number[], anchor: number): number {
	if (tops.length === 0) return -1;
	let active = 0;
	for (let i = 0; i < tops.length; i++) {
		if (tops[i] <= anchor) active = i;
	}
	return active;
}
