import { describe, expect, it } from "vitest";
import { DW, LABEL_W } from "./layout";
import { hoverColumn } from "./hover";

const view = { scrollerLeft: 100, canvasLeft: 100, days: 10 };

describe("hoverColumn", () => {
	it("snaps the pointer to the day column under it", () => {
		expect(hoverColumn({ ...view, clientX: 100 + LABEL_W })).toBe(0);
		expect(hoverColumn({ ...view, clientX: 100 + LABEL_W + DW - 1 })).toBe(0);
		expect(hoverColumn({ ...view, clientX: 100 + LABEL_W + DW })).toBe(1);
		expect(hoverColumn({ ...view, clientX: 100 + LABEL_W + 9 * DW + 5 })).toBe(9);
	});

	it("follows the scroll, which moves the canvas under a still pointer", () => {
		const scrolled = { ...view, canvasLeft: 100 - 3 * DW };
		expect(hoverColumn({ ...scrolled, clientX: 100 + LABEL_W })).toBe(3);
	});

	it("is none over the sticky label column, past the last day and before the first", () => {
		expect(hoverColumn({ ...view, clientX: 100 + LABEL_W - 1 })).toBeNull();
		expect(hoverColumn({ ...view, clientX: 100 + LABEL_W + 10 * DW })).toBeNull();
		expect(hoverColumn({ ...view, canvasLeft: 100 + 2 * DW, clientX: 100 + LABEL_W + DW })).toBeNull();
	});
});
