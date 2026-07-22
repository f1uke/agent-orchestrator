import { PROCS_BOX, PROCS_VIEW } from "./Procs";

// Sizes shared by the overlay WINDOW (main process) and the stage that draws into
// it (renderer). They live here, free of React, so the main process can size the
// window from the same numbers the art is drawn with instead of a guess.
//
// This exists because the guess was wrong: the band was 190px, a Proc's hover
// tooltip needed 218px, and it was clipped off the top of the window — so hovering
// a Proc appeared to do nothing at all.

/** Drawn Proc height. `full` tier from the design's size rules. */
export const PET_HEIGHT = 128;

/** The drawn frame for one Proc: figure plus the scenery either side and above. */
export function petFrame(size: number = PET_HEIGHT) {
	const scale = size / PROCS_BOX.height;
	return {
		width: PROCS_VIEW.width * scale,
		height: PROCS_VIEW.height * scale,
		offsetX: PROCS_VIEW.x * scale,
		overhangRight: (PROCS_VIEW.x + PROCS_VIEW.width - PROCS_BOX.width) * scale,
		figureWidth: PROCS_BOX.width * scale,
	};
}

/**
 * Headroom above a Proc for the things that sit over it: the hover tooltip (the
 * tallest, at four lines) and later the speech bubble.
 */
export const OVERHEAD_ALLOWANCE = 96;

/** Everything the overlay ever draws, stacked. The window must be at least this tall. */
export const COMPANION_CONTENT_HEIGHT = Math.ceil(petFrame().height + OVERHEAD_ALLOWANCE);
