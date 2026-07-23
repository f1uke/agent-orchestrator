import { describe, expect, it } from "vitest";
import {
	SHAKE_LEGS,
	SHAKE_MIN_LEG_PX,
	SHAKE_MIN_LEG_SPEED,
	SHAKE_WINDOW_MS,
	isShaking,
	newShakeTrack,
	trackShake,
	type ShakePoint,
	type ShakeTrack,
} from "./shake";

const T0 = 500_000;

/** Feed a whole gesture in, in order, and hand back the track it left behind. */
function play(points: ShakePoint[], from: ShakeTrack = newShakeTrack()): ShakeTrack {
	return points.reduce(trackShake, from);
}

/**
 * A gesture sampled the way a pointer actually is: a straight run from one point to
 * the next, cut into `steps` samples, so the detector sees the same dribble of small
 * deltas a real pointermove stream gives it rather than one clean jump.
 */
function swipe(
	from: { x: number; y: number; at: number },
	to: { x: number; y: number; at: number },
	steps = 4,
): ShakePoint[] {
	const points: ShakePoint[] = [];
	for (let i = 1; i <= steps; i++) {
		const t = i / steps;
		points.push({
			x: from.x + (to.x - from.x) * t,
			y: from.y + (to.y - from.y) * t,
			at: from.at + (to.at - from.at) * t,
		});
	}
	return points;
}

/**
 * A wiggle: `legs` reversals of `amplitude` px, each taking `legMs`. The default is a
 * comfortable hand shake — about 6Hz, 40px each way.
 */
function wiggle(
	options: { legs?: number; amplitude?: number; legMs?: number; axis?: "x" | "y"; at?: number; origin?: number } = {},
): ShakePoint[] {
	const { legs = SHAKE_LEGS, amplitude = 40, legMs = 70, axis = "x", at = T0, origin = 600 } = options;
	const points: ShakePoint[] = [];
	let cursor = { x: axis === "x" ? origin : 300, y: axis === "y" ? origin : 300, at };
	points.push(cursor);
	for (let leg = 0; leg < legs; leg++) {
		const step = leg % 2 === 0 ? amplitude : -amplitude;
		const next = {
			x: axis === "x" ? cursor.x + step : cursor.x,
			y: axis === "y" ? cursor.y + step : cursor.y,
			at: cursor.at + legMs,
		};
		points.push(...swipe(cursor, next));
		cursor = next;
	}
	return points;
}

describe("the shake gesture", () => {
	it("fires on a rapid back-and-forth wiggle", () => {
		expect(isShaking(play(wiggle()))).toBe(true);
	});

	it("reads a vertical wiggle as readily as a horizontal one", () => {
		// A hand told to shake something does not check which way the band runs.
		expect(isShaking(play(wiggle({ axis: "y" })))).toBe(true);
	});

	it("does NOT fire on a fling — the throw gesture must survive intact", () => {
		// One long directional run, fast enough to throw a Proc across the desktop.
		const fling = play(swipe({ x: 300, y: 300, at: T0 }, { x: 900, y: 240, at: T0 + 180 }, 12));

		expect(isShaking(fling)).toBe(false);
	});

	it("does NOT fire on a fling with a wind-up behind it", () => {
		// Pull back, then throw: two legs, which is the most a throw ever has.
		let track = play(swipe({ x: 600, y: 300, at: T0 }, { x: 480, y: 300, at: T0 + 120 }, 6));
		track = play(swipe({ x: 480, y: 300, at: T0 + 120 }, { x: 1000, y: 260, at: T0 + 300 }, 12), track);

		expect(isShaking(track)).toBe(false);
	});

	it("does NOT fire on a slow drag, however far it wanders", () => {
		// Carrying a Proc across the band and back again, twice, at a walking pace: the
		// reversals are all there, and none of them is a shake.
		const slow = play(wiggle({ amplitude: 220, legMs: 1_400 }));

		expect(isShaking(slow)).toBe(false);
	});

	it("does NOT fire on fine positioning jitter", () => {
		// Nudging a Proc into place: fast, reversing, and going nowhere.
		expect(isShaking(play(wiggle({ amplitude: 6, legMs: 40 })))).toBe(false);
	});

	it("does NOT fire on a hold that never moves at all", () => {
		const still = play(Array.from({ length: 20 }, (_, i) => ({ x: 600, y: 300, at: T0 + i * 16 })));

		expect(isShaking(still)).toBe(false);
	});

	it("needs three reversals, not one", () => {
		expect(isShaking(play(wiggle({ legs: SHAKE_LEGS - 1 })))).toBe(false);
		expect(isShaking(play(wiggle({ legs: SHAKE_LEGS })))).toBe(true);
	});

	it("forgets legs that have aged out of the window", () => {
		// Half a shake, a pause, and the other half is not a shake: the whole point of
		// the window is that the reversals have to be part of ONE gesture.
		let track = play(wiggle({ legs: 2 }));
		track = play(wiggle({ legs: 2, at: T0 + SHAKE_WINDOW_MS * 2, origin: 600 }), track);

		expect(isShaking(track)).toBe(false);
	});

	it("fires after a press-and-HOLD, which is how the gesture is actually made", () => {
		// The hand goes down, rests on the Proc for a moment, and only then shakes — and
		// a hand holding still emits NO pointer events, so the sample before the opening
		// flick is the press itself, half a second earlier. Measured from there that
		// flick looks as slow as the pause, and the whole gesture is thrown away.
		const press = play([{ x: 600, y: 300, at: T0 }]);

		expect(isShaking(play(wiggle({ at: T0 + 500 }), press))).toBe(true);
		// …and the pause itself is still not a shake.
		expect(isShaking(press)).toBe(false);
	});

	it("survives a noisy sample that stutters backwards mid-leg", () => {
		// A pointer stream is not clean. One 2px hiccup inside a 40px leg must not cut
		// the leg in two and disqualify a real shake.
		const noisy = wiggle().flatMap((point, index) =>
			index % 3 === 1 ? [point, { ...point, x: point.x - 2, at: point.at + 1 }] : [point],
		);

		expect(isShaking(play(noisy))).toBe(true);
	});

	it("starts clean, so the previous gesture cannot finish this one", () => {
		expect(isShaking(newShakeTrack())).toBe(false);
		expect(isShaking(play(wiggle({ legs: 2 }), newShakeTrack()))).toBe(false);
	});

	it("keeps the track pure — tracking a sample never mutates what it was given", () => {
		const before = play(wiggle({ legs: 3 }));
		const snapshot = JSON.stringify(before);
		trackShake(before, { x: 400, y: 300, at: T0 + 5_000 });

		expect(JSON.stringify(before)).toBe(snapshot);
	});

	it("holds a threshold a hand can clear and a drag cannot", () => {
		// The numbers themselves, pinned: a leg must be a real flick of the wrist.
		expect(SHAKE_MIN_LEG_PX).toBeGreaterThanOrEqual(16);
		expect(SHAKE_MIN_LEG_SPEED).toBeGreaterThan(0.2);
		expect(SHAKE_LEGS).toBeGreaterThanOrEqual(4);
	});
});
