import { describe, expect, it } from "vitest";
import { CAST } from "./cast";
import { PROP_COLOURS, PROCS_INK, PROCS_RIM_PX, contrastRatio, relativeLuminance, worstSeparation } from "./palette";

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

/** Every colour that is ever drawn against the wallpaper rather than against the pet. */
function exposedColours(): Array<{ what: string; colour: string }> {
	const fromCast = CAST.flatMap((member) => [
		{ what: `${member.name} body`, colour: member.body },
		{ what: `${member.name} shade`, colour: member.shade },
		{ what: `${member.name} hat`, colour: member.hatFill },
		{ what: `${member.name} hat trim`, colour: member.hatTrim },
	]);
	// Bubble TEXT is drawn on the bubble's own fill, never on the wallpaper, so it is
	// judged against that fill instead (see Bubble.test.tsx). Sweeping it here would
	// demand it be light enough to survive a dark desktop, i.e. unreadable on paper.
	const fromProps = Object.entries(PROP_COLOURS)
		.filter(([what]) => !what.startsWith("bubble"))
		.map(([what, colour]) => ({ what: `prop ${what}`, colour }));
	return [...fromCast, ...fromProps];
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
		expect(contrastRatio(CAST[0].body, CAST[0].body)).toBeCloseTo(1, 5);
	});
});

describe("wallpaper legibility", () => {
	it("proves the single-channel failure the rim exists to fix", () => {
		// Every cast colour disappears against SOME wallpaper on its own. This is the
		// measured fact the whole two-channel rule rests on, not an assumption — and
		// it is why no character may be judged by how it looks on one desktop.
		for (const { what, colour } of exposedColours()) {
			const alone = Math.min(...wallpapers().map((w) => contrastRatio(colour, w)));

			expect(alone, what).toBeLessThan(1.5);
		}
	});

	it("keeps every cast colour and every prop separable from ANY wallpaper", () => {
		// The regression this catches for real: #157's shade measured 2.96:1 and was
		// shipped, because the old test only ever swept the head colour.
		for (const { what, colour } of exposedColours()) {
			expect(worstSeparation(colour), what).toBeGreaterThanOrEqual(3);
		}
	});

	it("keeps the face readable on every character's head and body", () => {
		for (const member of CAST) {
			expect(contrastRatio(PROCS_INK, member.body), member.name).toBeGreaterThanOrEqual(4.5);
			expect(contrastRatio(PROCS_INK, member.shade), member.name).toBeGreaterThanOrEqual(4.5);
		}
	});

	it("keeps each character's blush visible on its own head without shouting", () => {
		for (const member of CAST) {
			const against = contrastRatio(member.blush, member.body);

			expect(against, member.name).toBeGreaterThan(1.35);
			expect(against, member.name).toBeLessThan(4.5);
		}
	});

	it("keeps the rim at the width the design measured it at", () => {
		expect(PROCS_RIM_PX).toBe(2.4);
	});
});

describe("telling the cast apart", () => {
	it("gives no two characters near-identical head colours", () => {
		// Six pale tints that measure the same are six of the same character as far
		// as a glance across the room is concerned.
		for (const a of CAST) {
			for (const b of CAST) {
				if (a.id === b.id) continue;
				expect(distance(a.body, b.body), `${a.name} vs ${b.name}`).toBeGreaterThan(40);
			}
		}
	});
});

/** Plain RGB distance. Crude on purpose: it is a "these are not the same swatch" guard. */
function distance(a: string, b: string): number {
	const parse = (hex: string) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
	const [ar, ag, ab] = parse(a);
	const [br, bg, bb] = parse(b);
	return Math.sqrt((ar - br) ** 2 + (ag - bg) ** 2 + (ab - bb) ** 2);
}
