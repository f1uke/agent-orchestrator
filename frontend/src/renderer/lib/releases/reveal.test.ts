import { describe, expect, it } from "vitest";
import { scrollDelta } from "./reveal";

const view = { left: 280, top: 46, right: 1000, bottom: 700 };

describe("scrollDelta", () => {
	it("leaves a target that is already in view alone", () => {
		expect(scrollDelta({ left: 400, top: 100, right: 500, bottom: 120 }, view)).toEqual({ dx: 0, dy: 0 });
	});

	it("brings a target hidden under the label column or the axis out with a margin", () => {
		expect(scrollDelta({ left: 200, top: 10, right: 260, bottom: 30 }, view)).toEqual({ dx: -104, dy: -60 });
	});

	it("brings a target past the right or bottom edge in with a margin", () => {
		expect(scrollDelta({ left: 1100, top: 800, right: 1150, bottom: 820 }, view)).toEqual({ dx: 174, dy: 144 });
	});

	it("aligns a target wider or taller than the view to its start", () => {
		expect(scrollDelta({ left: 900, top: 600, right: 2000, bottom: 1500 }, view)).toEqual({ dx: 596, dy: 530 });
	});

	it("does not scroll sideways for a target in the sticky label column", () => {
		expect(scrollDelta({ left: 0, top: 800, right: 280, bottom: 900 }, view, { sticky: true })).toEqual({
			dx: 0,
			dy: 224,
		});
	});
});
