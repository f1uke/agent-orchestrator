import { describe, expect, it } from "vitest";
import { COMPANION_CONTENT_HEIGHT, figureLeft, petFrame } from "./layout";

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
