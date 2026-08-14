import { describe, expect, it } from "vitest";
import { devicePoint, fitScreen } from "./screen-fit";

// 280px is the inspector rail's real content floor, so it is the width the pane
// has to stay usable at rather than a round number.
const NARROW = 280;

const portrait = { width: 1320, height: 2868 };
const landscape = { width: 2868, height: 1320 };
const watch = { width: 396, height: 484 };

function ratio({ width, height }: { width: number; height: number }) {
	return width / height;
}

describe("fitScreen", () => {
	it("keeps the device's shape at every pane size, including the narrowest one", () => {
		const boxes = [
			{ width: NARROW, height: 200 },
			{ width: NARROW, height: 600 },
			{ width: NARROW, height: 1200 },
			{ width: 420, height: 700 },
			{ width: 900, height: 400 },
		];
		for (const box of boxes) {
			for (const frame of [portrait, landscape, watch]) {
				const fitted = fitScreen(box, frame);
				expect(ratio(fitted)).toBeCloseTo(ratio(frame), 5);
			}
		}
	});

	it("uses all of the space in whichever direction runs out first", () => {
		// A tall pane and a tall screen: width is the limit.
		const tall = fitScreen({ width: NARROW, height: 1200 }, portrait);
		expect(tall.width).toBeCloseTo(NARROW, 5);
		expect(tall.height).toBeLessThanOrEqual(1200);

		// A short pane and the same screen: height is the limit.
		const short = fitScreen({ width: NARROW, height: 200 }, portrait);
		expect(short.height).toBeCloseTo(200, 5);
		expect(short.width).toBeLessThanOrEqual(NARROW);
	});

	it("never overflows the pane it was given", () => {
		for (const frame of [portrait, landscape, watch]) {
			const fitted = fitScreen({ width: NARROW, height: 640 }, frame);
			expect(fitted.width).toBeLessThanOrEqual(NARROW + 1e-9);
			expect(fitted.height).toBeLessThanOrEqual(640 + 1e-9);
			expect(fitted.left).toBeGreaterThanOrEqual(0);
			expect(fitted.top).toBeGreaterThanOrEqual(0);
		}
	});

	// A small framebuffer in a big pane is the case a "shrink to fit" rule gets
	// wrong: the watch screen has to grow into the space, not sit in the middle
	// at its native size while the pane is mostly empty.
	it("scales a small screen up rather than leaving the pane empty", () => {
		const fitted = fitScreen({ width: 600, height: 900 }, watch);
		expect(fitted.width).toBeCloseTo(600, 5);
		expect(fitted.height).toBeGreaterThan(watch.height);
	});

	it("has nothing to draw when the pane has no size yet", () => {
		expect(fitScreen({ width: 0, height: 0 }, portrait)).toEqual({ width: 0, height: 0, left: 0, top: 0 });
	});
});

describe("devicePoint", () => {
	const box = { width: NARROW, height: 1200 };

	it("maps the middle of the picture to the middle of the device", () => {
		const fitted = fitScreen(box, portrait);
		const point = devicePoint(box, portrait, {
			x: fitted.left + fitted.width / 2,
			y: fitted.top + fitted.height / 2,
		});
		expect(point?.x).toBeCloseTo(0.5, 5);
		expect(point?.y).toBeCloseTo(0.5, 5);
	});

	it("maps the picture's corners to the device's corners", () => {
		const fitted = fitScreen(box, portrait);
		expect(devicePoint(box, portrait, { x: fitted.left, y: fitted.top })).toEqual({ x: 0, y: 0 });
		const bottomRight = devicePoint(box, portrait, {
			x: fitted.left + fitted.width,
			y: fitted.top + fitted.height,
		});
		expect(bottomRight?.x).toBeCloseTo(1, 5);
		expect(bottomRight?.y).toBeCloseTo(1, 5);
	});

	// The bars beside a letterboxed screen are not the screen. Clamping a click
	// there onto the nearest edge would send a tap the human never aimed.
	it("refuses a click in the bars beside the picture", () => {
		const wide = { width: 900, height: 400 };
		const fitted = fitScreen(wide, portrait);
		expect(fitted.left).toBeGreaterThan(0);
		expect(devicePoint(wide, portrait, { x: fitted.left / 2, y: 200 })).toBeNull();
		expect(devicePoint(wide, portrait, { x: wide.width - fitted.left / 2, y: 200 })).toBeNull();
	});

	it("refuses a click before the pane has a size", () => {
		expect(devicePoint({ width: 0, height: 0 }, portrait, { x: 1, y: 1 })).toBeNull();
	});
});
