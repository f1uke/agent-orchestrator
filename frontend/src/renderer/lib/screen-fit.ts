/**
 * Where a device screen actually sits inside the box it is drawn in.
 *
 * The canvas fills the pane and letterboxes its picture (`object-fit: contain`),
 * which is the only way to fill the space in both orientations and for every
 * framebuffer from a watch to an iPad without either distorting the picture or
 * refusing to scale it up. The cost is that the element's box is not the
 * picture's box, so a click has to be mapped through the letterbox rather than
 * straight off the element's rectangle - and a click that lands in the bars
 * beside the picture is not a click on the device at all.
 *
 * This lives apart from the panel because it is the half of "the screen fills
 * the pane, keeping its aspect ratio" that can be checked without a layout
 * engine: jsdom has none, and this repo has already shipped controls that
 * rendered, passed every test and were never hittable.
 */

export type Box = { width: number; height: number };

export type FittedScreen = {
	/** The picture's own size once scaled to fit. */
	width: number;
	height: number;
	/** Where it starts inside the box - half the leftover space on each side. */
	left: number;
	top: number;
};

/**
 * fitScreen scales a framebuffer to fill as much of a box as it can without
 * changing its shape. It scales up as readily as down: a watch screen in a tall
 * pane should fill it, not sit in the middle at its native size.
 */
export function fitScreen(box: Box, frame: Box): FittedScreen {
	if (box.width <= 0 || box.height <= 0 || frame.width <= 0 || frame.height <= 0) {
		return { width: 0, height: 0, left: 0, top: 0 };
	}
	const scale = Math.min(box.width / frame.width, box.height / frame.height);
	const width = frame.width * scale;
	const height = frame.height * scale;
	return { width, height, left: (box.width - width) / 2, top: (box.height - height) / 2 };
}

/**
 * devicePoint turns a pointer position inside the canvas box into the
 * normalized 0..1 coordinate the HID layer takes - the same coordinate space
 * `ao sim ax` reports per element, so nothing in between needs pixels.
 *
 * It returns null for a point in the letterbox. Clamping instead would turn a
 * click beside the screen into a tap on its edge, which is a gesture the human
 * did not ask for on a device somebody else may be watching.
 */
export function devicePoint(box: Box, frame: Box, point: { x: number; y: number }): { x: number; y: number } | null {
	const fitted = fitScreen(box, frame);
	if (fitted.width <= 0 || fitted.height <= 0) return null;
	const x = (point.x - fitted.left) / fitted.width;
	const y = (point.y - fitted.top) / fitted.height;
	// The picture's own edges land a rounding error outside 0..1, and a click on
	// the last row of pixels is a click on the screen. The tolerance is a
	// hair's width, not a way in from the bars.
	const edge = 1e-9;
	if (x < -edge || x > 1 + edge || y < -edge || y > 1 + edge) return null;
	return { x: clamp01(x), y: clamp01(y) };
}

function clamp01(n: number): number {
	return Math.min(1, Math.max(0, n));
}

/**
 * fitDevice works out how big the screen may be drawn and how thick its own
 * body is around it - together, because they depend on each other.
 *
 * They were computed apart, and disagreed: the fit reserved a fixed four pixels
 * for the body while the body drew a thickness of its own, so the frame came
 * out larger than the space it was fitted into, got clamped, and the picture
 * letterboxed inside it with dark bands top and bottom. One function returns
 * both so they cannot drift again.
 *
 * The proportions are the device's own, read from the artwork Xcode ships (see
 * internal/simchrome) - an iPhone 17 Pro's body is 4.5% of its screen's width
 * and its display's corners 15.5%, and both differ per model. A device this
 * machine has no artwork for gets no body at all, because a guessed one was
 * visibly wrong.
 */
export function fitDevice(
	stage: { width: number; height: number } | null,
	screen: { width: number; height: number } | null,
	frame: { thickness: number; radius: number } | null,
) {
	if (!stage || !screen) return null;
	const thickness = frame?.thickness ?? 0;
	// The body is a fraction of the screen's width, and the screen is what is
	// left of the stage once the body is taken off it - so each depends on the
	// other. Solved rather than iterated: rounding gives the iteration no fixed
	// point (18, 16, 17, 16, … for one real case), so it would end wherever it
	// was stopped.
	//
	// With w the picture's width unconstrained by any body, b = w·t/(1 + 2t) is
	// the body that leaves exactly the picture it was measured from.
	const bare = Math.min(stage.width, (stage.height * screen.width) / screen.height);
	const bezel = Math.max(0, Math.round((bare * thickness) / (1 + 2 * thickness)));
	const fitted = fitScreen({ width: stage.width - bezel * 2, height: stage.height - bezel * 2 }, screen);
	if (fitted.width <= 0 || fitted.height <= 0) return null;
	const radius = Math.round(fitted.width * (frame?.radius ?? 0));
	return { screen: fitted, bezel, radius, outerRadius: radius + bezel };
}
