// The hover-hold that opens a Proc's tooltip.
//
// Pure state plus two readers, so the delay can be tested without a clock and the
// renderer holds nothing but a timestamp.
//
// The delay is the whole point: a tooltip that fires on contact would flash at
// every Proc the pointer crosses on the way to something else, and the overlay
// spans the width of the screen, so that is most journeys across the desktop.

/**
 * How long the pointer must rest on a Proc before it explains itself.
 *
 * Long enough that it does not fire at every Proc the pointer crosses on the way
 * somewhere else — the overlay spans the whole width of the screen, so that is most
 * journeys across the desktop — and short enough that deliberately pointing at one
 * feels answered rather than waited on. Set by the human at a second.
 */
export const HOVER_TOOLTIP_DELAY_MS = 1_000;

export type HoverState = {
	/** The Proc under the pointer, or null. */
	petId: string | null;
	/** When the pointer arrived on THIS Proc. */
	since: number;
};

export function idleHover(): HoverState {
	return { petId: null, since: 0 };
}

/**
 * Record where the pointer is. Staying on the same Proc keeps the original arrival
 * time — otherwise every mouse tremor inside a Proc would restart the countdown and
 * the tooltip would never open for anyone whose hand is not perfectly still.
 */
export function hoverAt(state: HoverState, petId: string | null, now: number): HoverState {
	if (petId === state.petId) return state;
	return { petId, since: now };
}

/** The Proc whose tooltip should be open, if the pointer has rested long enough. */
export function tooltipTarget(state: HoverState, now: number): string | null {
	if (!state.petId) return null;
	return now - state.since >= HOVER_TOOLTIP_DELAY_MS ? state.petId : null;
}
