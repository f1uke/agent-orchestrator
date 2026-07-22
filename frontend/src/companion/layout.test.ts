import { describe, expect, it } from "vitest";
import {
	COMPANION_CONTENT_HEIGHT,
	figureLeft,
	inkFloorGap,
	NAME_TAG_ALLOWANCE,
	NAME_TAG_HEIGHT,
	petFrame,
} from "./layout";

describe("figureLeft", () => {
	it("is flush with the frame when the Proc faces you", () => {
		expect(figureLeft(false)).toBe(0);
	});

	it("follows the figure across the frame when the sprite mirrors", () => {
		// The figure is not centred in its frame: the frame carries scenery room on the
		// cord side. Mirroring therefore MOVES the figure, and a name chip pinned at
		// the frame's left edge ends up ~39px off the Proc it names — which is exactly
		// what the human saw on the one Proc that happened to be walking left.
		const shift = figureLeft(true);

		expect(shift).toBeGreaterThan(30);
		expect(shift).toBeLessThan(48);
	});

	it("keeps the mirrored figure inside its own frame", () => {
		const frame = petFrame();

		expect(figureLeft(true) + frame.figureWidth).toBeLessThanOrEqual(frame.offsetX + frame.width + 0.001);
	});
});

describe("COMPANION_CONTENT_HEIGHT", () => {
	it("leaves room above a Proc for the tooltip that sits over it", () => {
		expect(COMPANION_CONTENT_HEIGHT).toBeGreaterThan(petFrame().height + 60);
	});
});

describe("room for the name chip", () => {
	// The chip used to sit ON the bottom of the drawing, covering the plug at the end
	// of the cord — and the cord is how a Proc says whether its session is still
	// connected, so the label was hiding a state. Lifting the cast off it then left
	// nine pixels of nothing between a Proc and its own label, because the frame runs
	// seven units below the lowest thing actually drawn in it.
	const chipTopAboveProcBottom = 2 + NAME_TAG_HEIGHT;
	const lowestInkAboveProcBottom = NAME_TAG_ALLOWANCE + inkFloorGap();

	it("never lets the chip reach the lowest thing the Proc draws", () => {
		expect(lowestInkAboveProcBottom).toBeGreaterThan(chipTopAboveProcBottom);
	});

	it("keeps the chip close enough to read as this Proc's label", () => {
		expect(lowestInkAboveProcBottom - chipTopAboveProcBottom).toBeLessThanOrEqual(5);
	});

	it("makes the window tall enough for everything stacked in it", () => {
		expect(COMPANION_CONTENT_HEIGHT).toBeGreaterThanOrEqual(petFrame().height + NAME_TAG_ALLOWANCE);
	});
});
