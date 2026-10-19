import { describe, expect, it } from "vitest";
import { grow, intersect, runs, scrollDelta } from "./reveal";

const view = { left: 280, top: 46, right: 1000, bottom: 700 };

describe("scrollDelta", () => {
	it("centres a target that is already in view, so every press lands it in the same place", () => {
		expect(scrollDelta([{ left: 400, top: 100, right: 500, bottom: 120 }], view)).toEqual({ dx: -190, dy: -263 });
		expect(scrollDelta([{ left: 590, top: 363, right: 690, bottom: 383 }], view)).toEqual({ dx: 0, dy: 0 });
	});

	it("centres a target hidden under the label column, the axis or past an edge", () => {
		expect(scrollDelta([{ left: 200, top: 10, right: 260, bottom: 30 }], view)).toEqual({ dx: -410, dy: -353 });
		expect(scrollDelta([{ left: 1100, top: 800, right: 1150, bottom: 820 }], view)).toEqual({ dx: 485, dy: 437 });
	});

	it("centres a set of targets that fits as one", () => {
		const set = [
			{ left: 600, top: 800, right: 700, bottom: 820 },
			{ left: 600, top: 900, right: 700, bottom: 920 },
		] as const;
		expect(scrollDelta(set, view)).toEqual({ dx: 10, dy: 487 });
	});

	it("puts the first target at the top left with a margin when the set does not fit", () => {
		expect(scrollDelta([{ left: 900, top: 600, right: 2000, bottom: 1500 }], view)).toEqual({ dx: 596, dy: 530 });
		const tall = [
			{ left: 600, top: 800, right: 700, bottom: 820 },
			{ left: 600, top: 1600, right: 700, bottom: 1620 },
		] as const;
		expect(scrollDelta(tall, view).dy).toBe(800 - 46 - 24);
	});

	it("does not scroll sideways for a target in the sticky label column", () => {
		expect(scrollDelta([{ left: 0, top: 800, right: 280, bottom: 900 }], view, { sticky: true })).toEqual({
			dx: 0,
			dy: 477,
		});
	});
});

describe("intersect and grow", () => {
	it("keeps the part of a box inside another, and nothing when they do not meet", () => {
		expect(intersect({ left: 0, top: 0, right: 100, bottom: 50 }, view)).toBeNull();
		expect(intersect({ left: 200, top: 40, right: 400, bottom: 60 }, view)).toEqual({
			left: 280,
			top: 46,
			right: 400,
			bottom: 60,
		});
	});

	it("grows a box evenly on all four sides", () => {
		expect(grow({ left: 10, top: 20, right: 30, bottom: 40 }, 5)).toEqual({ left: 5, top: 15, right: 35, bottom: 45 });
	});
});

describe("runs", () => {
	it("groups rows of one list that touch into one run, and keeps apart rows of another list or with a gap", () => {
		const row = (left: number, top: number) => ({ left, top, right: left + 300, bottom: top + 24 });
		expect(runs([row(10, 48), row(10, 0), row(10, 24), row(10, 100), row(400, 24)])).toEqual([[1, 2, 0], [4], [3]]);
	});
});
