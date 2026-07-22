import { describe, expect, it } from "vitest";
import { PROCS_BODY, PROCS_BODY_SHADE, PROCS_INK, PROCS_RIM_PX, contrastRatio, relativeLuminance } from "./palette";

// Contrast is a pure function of relative luminance, so sweeping the grey axis
// from black to white covers EVERY possible wallpaper colour, not just greys.
function wallpapers(steps = 200): string[] {
	return Array.from({ length: steps + 1 }, (_, i) => {
		const c = Math.round((i / steps) * 255)
			.toString(16)
			.padStart(2, "0");
		return `#${c}${c}${c}`;
	});
}

describe("relativeLuminance", () => {
	it("matches the WCAG anchors", () => {
		expect(relativeLuminance("#000000")).toBeCloseTo(0, 5);
		expect(relativeLuminance("#ffffff")).toBeCloseTo(1, 5);
	});
});

describe("contrastRatio", () => {
	it("is 21:1 for black on white and 1:1 for a colour on itself", () => {
		expect(contrastRatio("#000000", "#ffffff")).toBeCloseTo(21, 2);
		expect(contrastRatio(PROCS_BODY, PROCS_BODY)).toBeCloseTo(1, 5);
	});
});

describe("wallpaper legibility", () => {
	it("proves the single-channel failure the rim exists to fix", () => {
		// The body alone disappears against some wallpaper — this is the measured
		// fact behind the design's rule, not an assumption.
		const worstBodyOnly = Math.min(...wallpapers().map((w) => contrastRatio(PROCS_BODY, w)));

		expect(worstBodyOnly).toBeLessThan(1.5);
	});

	it("keeps a Proc separable from any wallpaper through body OR rim", () => {
		for (const wallpaper of wallpapers()) {
			const separation = Math.max(contrastRatio(PROCS_BODY, wallpaper), contrastRatio(PROCS_INK, wallpaper));
			expect(separation).toBeGreaterThanOrEqual(3);
		}
	});

	it("keeps the face readable against the body it is drawn on", () => {
		expect(contrastRatio(PROCS_INK, PROCS_BODY)).toBeGreaterThanOrEqual(4.5);
		expect(contrastRatio(PROCS_INK, PROCS_BODY_SHADE)).toBeGreaterThanOrEqual(4.5);
	});

	it("keeps the rim at the width the design measured it at", () => {
		expect(PROCS_RIM_PX).toBe(2.4);
	});
});
