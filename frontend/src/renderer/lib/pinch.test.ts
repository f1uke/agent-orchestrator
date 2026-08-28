import { describe, expect, it } from "vitest";
import { MIN_PINCH_SPAN, PINCH_ANCHOR, pannedAnchor, pinchGrip, pinchSpan } from "./pinch";

// The numbers here are Simulator.app's own, measured rather than remembered: a
// page in the simulator's Safari logged every touch while synthetic Option+mouse
// events were posted to the Simulator window. On a 440x956 device every frame of
// every Option-drag had its two contacts' midpoint at (220, 478) - the exact
// centre of the screen - however far the pointer had travelled.
describe("pinchGrip", () => {
	it("puts one finger under the pointer and mirrors the other through the anchor", () => {
		expect(pinchGrip({ x: 0.5, y: 0.75 }, PINCH_ANCHOR)).toEqual({
			a: { x: 0.5, y: 0.75 },
			b: { x: 0.5, y: 0.25 },
		});
	});

	it("keeps the midpoint on the anchor however far the pointer goes", () => {
		for (const at of [
			{ x: 0.1, y: 0.1 },
			{ x: 0.93, y: 0.42 },
			{ x: 0, y: 1 },
		]) {
			const grip = pinchGrip(at, PINCH_ANCHOR);
			expect((grip.a.x + grip.b!.x) / 2).toBeCloseTo(PINCH_ANCHOR.x, 9);
			expect((grip.a.y + grip.b!.y) / 2).toBeCloseTo(PINCH_ANCHOR.y, 9);
		}
	});

	// Moving away from the anchor spreads (zoom in); moving towards it pinches
	// (zoom out). There is no separate distance to invent a default for.
	it("spreads as the pointer leaves the anchor and closes as it returns", () => {
		const gap = (at: { x: number; y: number }) => {
			const g = pinchGrip(at, PINCH_ANCHOR);
			return Math.hypot(g.b!.x - g.a.x, g.b!.y - g.a.y);
		};
		expect(gap({ x: 0.5, y: 0.9 })).toBeGreaterThan(gap({ x: 0.5, y: 0.7 }));
		expect(gap({ x: 0.5, y: 0.55 })).toBeLessThan(gap({ x: 0.5, y: 0.7 }));
	});

	// With the anchor in the middle a mirrored finger is always on the screen, so
	// this only bites once Shift has moved the anchor. Then a clamp is the honest
	// answer: a finger cannot be off the screen, the overlay draws it where it
	// actually is, and the alternative is a coordinate the daemon refuses -
	// which would drop the pinch mid-gesture.
	it("keeps a mirrored finger on the screen when the anchor has been moved", () => {
		const grip = pinchGrip({ x: 0.9, y: 0.5 }, { x: 0.2, y: 0.5 });
		expect(grip.b).toEqual({ x: 0, y: 0.5 });
	});
});

// Shift moves both fingers together instead of apart, which is the only way to
// zoom about anywhere but the middle of the screen. Measured in Simulator.app:
// with Shift added mid-pinch both contacts moved by the same delta and their
// midpoint left the centre.
describe("pannedAnchor", () => {
	it("carries the second finger along with the pointer instead of spreading", () => {
		const from = { x: 0.5, y: 0.75 };
		const to = { x: 0.7, y: 0.75 };
		const before = pinchGrip(from, PINCH_ANCHOR);
		const after = pinchGrip(to, pannedAnchor(PINCH_ANCHOR, from, to));

		expect(after.a.x - before.a.x).toBeCloseTo(0.2, 9);
		expect(after.b!.x - before.b!.x).toBeCloseTo(0.2, 9);
		// The fingers are exactly as far apart as they were.
		expect(after.a.y - after.b!.y).toBeCloseTo(before.a.y - before.b!.y, 9);
	});

	it("does not let the anchor leave the screen", () => {
		expect(pannedAnchor(PINCH_ANCHOR, { x: 0.5, y: 0.5 }, { x: 0.5, y: 2 })).toEqual({ x: 0.5, y: 1 });
	});
});

// The anchor starts at the middle of the screen, so a press ON the middle puts
// both contacts on the same spot - which the daemon refuses, correctly, because
// a pinch that lands as one touch sends events and changes nothing. The pane
// waits instead of being told off, so this is the number that decides when it
// stops waiting.
describe("pinchSpan", () => {
	it("is zero when the pointer is exactly on the anchor", () => {
		expect(pinchSpan(pinchGrip(PINCH_ANCHOR, PINCH_ANCHOR))).toBe(0);
	});

	it("crosses the minimum a hair away from the anchor, and not before", () => {
		// The gap is twice the pointer's distance from the anchor, so half the
		// minimum is exactly the crossing point.
		const justInside = { x: PINCH_ANCHOR.x, y: PINCH_ANCHOR.y + MIN_PINCH_SPAN / 2 - 1e-6 };
		const justOutside = { x: PINCH_ANCHOR.x, y: PINCH_ANCHOR.y + MIN_PINCH_SPAN / 2 + 1e-6 };
		expect(pinchSpan(pinchGrip(justInside, PINCH_ANCHOR))).toBeLessThan(MIN_PINCH_SPAN);
		expect(pinchSpan(pinchGrip(justOutside, PINCH_ANCHOR))).toBeGreaterThan(MIN_PINCH_SPAN);
	});

	it("is zero for one finger, which has no span at all", () => {
		expect(pinchSpan({ a: { x: 0.2, y: 0.2 } })).toBe(0);
	});
});
