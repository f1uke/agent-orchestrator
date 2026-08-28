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
});

// The anchor starts at the middle of the screen, so the two contacts meet when
// the pointer reaches the middle - on a press that lands there, and again on a
// pinch dragged all the way closed through it. The daemon refuses a pair that
// close (it would land as one touch), and a refusal there is a red message and
// a device that answers nothing until the watchdog lifts it. So the fingers
// stop coming together instead, which is what real fingers do.
describe("the minimum the contacts may be apart", () => {
	it("holds the pair apart when the pointer is exactly on the anchor", () => {
		const grip = pinchGrip(PINCH_ANCHOR, PINCH_ANCHOR);
		expect(pinchSpan(grip)).toBeGreaterThan(MIN_PINCH_SPAN);
		// Vertical, because a phone is taller than it is wide and fingers that
		// have closed to nothing still have to point somewhere.
		expect(grip.a.x).toBeCloseTo(PINCH_ANCHOR.x, 9);
		expect(grip.b!.x).toBeCloseTo(PINCH_ANCHOR.x, 9);
	});

	it("holds the pair apart along the direction the pointer came from", () => {
		// A pointer a tenth of the minimum away, on the diagonal: the contacts
		// come out to the minimum along that same diagonal rather than snapping
		// to an axis, so the gesture keeps the shape the hand gave it.
		const nudge = MIN_PINCH_SPAN / 20;
		const grip = pinchGrip({ x: 0.5 + nudge, y: 0.5 + nudge }, PINCH_ANCHOR);
		expect(pinchSpan(grip)).toBeGreaterThan(MIN_PINCH_SPAN);
		expect(grip.a.x - 0.5).toBeCloseTo(grip.a.y - 0.5, 9);
		expect(grip.a.x).toBeGreaterThan(0.5);
	});

	it("leaves a pointer that is already far enough exactly where it is", () => {
		const at = { x: 0.32, y: 0.81 };
		expect(pinchGrip(at, PINCH_ANCHOR).a).toEqual(at);
	});

	// ⚠ The floor is only safe while the anchor has room for it: the contacts sit
	// half a span either side, so an anchor closer to an edge than that would
	// push one of them off the screen - and an off-screen contact is a
	// coordinate the daemon refuses, which drops the pinch mid-gesture.
	it("keeps a panned anchor far enough from the edges for the pair to fit", () => {
		const anchor = pannedAnchor(PINCH_ANCHOR, { x: 0.5, y: 0.5 }, { x: 2, y: -2 });
		expect(anchor.x).toBeLessThan(1);
		expect(anchor.y).toBeGreaterThan(0);
		// And a pinch about that anchor still puts both contacts on the screen.
		for (const at of [anchor, { x: 1, y: 0 }, { x: 0, y: 1 }]) {
			const grip = pinchGrip(at, anchor);
			for (const contact of [grip.a, grip.b!]) {
				expect(contact.x).toBeGreaterThanOrEqual(0);
				expect(contact.x).toBeLessThanOrEqual(1);
				expect(contact.y).toBeGreaterThanOrEqual(0);
				expect(contact.y).toBeLessThanOrEqual(1);
			}
		}
	});

	// ⚠ Every angle, not one: the contacts are placed by trigonometry and the
	// daemon compares a distance against the threshold, so a pair placed at
	// exactly the threshold can round to either side of it. The margin in
	// HELD_APART_RADIUS is what makes "held apart" mean "accepted", and this is
	// what says it does.
	it("clears the daemon's threshold at every angle, never merely reaches it", () => {
		for (let i = 0; i < 360; i += 1) {
			const angle = (i * Math.PI) / 180;
			// A pointer a thousandth of a span from the anchor, so the floor is
			// always what places the contacts.
			const at = {
				x: 0.5 + Math.cos(angle) * MIN_PINCH_SPAN * 0.001,
				y: 0.5 + Math.sin(angle) * MIN_PINCH_SPAN * 0.001,
			};
			expect(pinchSpan(pinchGrip(at, PINCH_ANCHOR))).toBeGreaterThan(MIN_PINCH_SPAN);
		}
	});

	it("reports no span at all for one finger, which has none", () => {
		expect(pinchSpan({ a: { x: 0.2, y: 0.2 } })).toBe(0);
	});
});
