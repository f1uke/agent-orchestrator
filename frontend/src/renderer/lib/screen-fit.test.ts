import { describe, expect, it } from "vitest";
import { devicePoint, fitDevice, fitScreen } from "./screen-fit";

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

describe("fitDevice", () => {
	// The bug this exists to make impossible: the screen was fitted into a box
	// that reserved a fixed four pixels for the body while the body drew a
	// thickness of its own, so the whole thing came out bigger than the space,
	// got clamped by CSS, and the picture letterboxed inside it with dark bands.
	it("never draws a device larger than the space it was given", () => {
		const frames = [
			{ thickness: 0.045, radius: 0.155 },
			{ thickness: 0.0411, radius: 0.1416 },
			{ thickness: 0.12, radius: 0.35 },
			{ thickness: 0, radius: 0 },
		];
		const stages = [
			{ width: NARROW, height: 640 },
			{ width: NARROW, height: 200 },
			{ width: 900, height: 400 },
			{ width: 420, height: 1200 },
		];
		for (const frame of frames) {
			for (const stage of stages) {
				for (const screen of [portrait, landscape, watch]) {
					const drawn = fitDevice(stage, screen, frame);
					if (!drawn) continue;
					const outerWidth = drawn.screen.width + drawn.bezel * 2;
					const outerHeight = drawn.screen.height + drawn.bezel * 2;
					expect(outerWidth).toBeLessThanOrEqual(stage.width + 1);
					expect(outerHeight).toBeLessThanOrEqual(stage.height + 1);
				}
			}
		}
	});

	it("keeps the screen's own shape whatever the body around it", () => {
		for (const screen of [portrait, landscape, watch]) {
			const drawn = fitDevice({ width: NARROW, height: 640 }, screen, { thickness: 0.045, radius: 0.155 });
			expect(drawn).not.toBeNull();
			expect(drawn!.screen.width / drawn!.screen.height).toBeCloseTo(screen.width / screen.height, 4);
		}
	});

	// A device this machine has no artwork for is drawn without a body, rather
	// than with a guessed one - a guess was visibly wrong twice.
	it("draws no body for a device with no frame of its own", () => {
		const drawn = fitDevice({ width: NARROW, height: 640 }, portrait, null);
		expect(drawn?.bezel).toBe(0);
		expect(drawn?.radius).toBe(0);
		expect(drawn?.screen.width).toBeCloseTo(NARROW, 5);
	});

	// The body and the corners are the device's own proportions, not a constant,
	// and the body is the one the picture it surrounds actually calls for -
	// within the pixel that rounding costs.
	it("takes the body and the corners from the device's own proportions", () => {
		for (const stage of [
			{ width: 400, height: 2000 },
			{ width: NARROW, height: 640 },
			{ width: 900, height: 400 },
		]) {
			const drawn = fitDevice(stage, portrait, { thickness: 0.045, radius: 0.155 });
			expect(drawn).not.toBeNull();
			expect(Math.abs(drawn!.bezel - drawn!.screen.width * 0.045)).toBeLessThanOrEqual(1);
			expect(drawn!.radius).toBe(Math.round(drawn!.screen.width * 0.155));
			expect(drawn!.outerRadius).toBe(drawn!.radius + drawn!.bezel);
		}
	});

	it("has nothing to draw before the pane or the screen is known", () => {
		expect(fitDevice(null, portrait, null)).toBeNull();
		expect(fitDevice({ width: NARROW, height: 640 }, null, null)).toBeNull();
		expect(fitDevice({ width: 0, height: 0 }, portrait, null)).toBeNull();
	});
});
