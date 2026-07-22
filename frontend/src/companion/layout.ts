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

/** The name chip's drawn height: 10px text at 1.4, inside 1px padding and the 2.4px rim. */
export const NAME_TAG_HEIGHT = 20;
/** Air below the chip, so it does not sit flush on the screen edge. */
const NAME_TAG_AIR = 2;
/** Air between the chip and the Proc's lowest ink. Small: the chip labels THIS Proc. */
const NAME_TAG_CLEARANCE = 3;

/**
 * The rig's lowest PAINTED y, across all fifteen scenes.
 *
 * Not the frame bottom, which is 132. Nothing is drawn below 125 — the cord's
 * plug is the lowest thing there is — and that seven-unit tail of empty box is
 * what made the chip look adrift the moment the art was lifted off it. Measured
 * in the browser over every state, and pinned by a test.
 */
export const LOWEST_INK_Y = 125;

/** Empty box below the lowest ink, in px. The chip is allowed to sit in it. */
export function inkFloorGap(size: number = PET_HEIGHT): number {
	const scale = size / PROCS_BOX.height;
	return (PROCS_VIEW.y + PROCS_VIEW.height - LOWEST_INK_Y) * scale;
}

/**
 * Room UNDER a Proc for its name chip.
 *
 * The chip used to be laid over the bottom of the drawing, where it covered the
 * plug on the end of the cord — and the cord is how a Proc says whether its
 * session is still connected, so the label was hiding a state. The whole cast
 * stands this much higher instead: reserve the space rather than overlap into it.
 *
 * Reduced by the empty tail of the frame, because reserving the full chip height
 * BELOW the box put nine pixels of nothing between a Proc and its own label.
 */
export const NAME_TAG_ALLOWANCE = Math.round(NAME_TAG_HEIGHT + NAME_TAG_AIR + NAME_TAG_CLEARANCE - inkFloorGap());

/** Everything the overlay ever draws, stacked. The window must be at least this tall. */
export const COMPANION_CONTENT_HEIGHT = Math.ceil(petFrame().height + OVERHEAD_ALLOWANCE + NAME_TAG_ALLOWANCE);

/**
 * Where the FIGURE's left edge sits inside the drawn frame, in px.
 *
 * The figure is not centred in its frame — the frame carries ~48 units of scenery
 * room on the cord side — so mirroring the sprite to walk left moves the figure to
 * the other side of that frame. Chrome pinned under the Proc (the name chip, the
 * hover tooltip) is NOT part of the sprite and does not mirror, so it has to be
 * told where the figure actually went. Measured: 39px, which is exactly what the
 * off-centre name chip was out by.
 */
export function figureLeft(mirrored: boolean, size: number = PET_HEIGHT): number {
	if (!mirrored) return 0;
	const frame = petFrame(size);
	return 2 * frame.offsetX + frame.width - frame.figureWidth;
}
