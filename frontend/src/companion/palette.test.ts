import { describe, expect, it } from "vitest";
import { ALL_LOOKS, looksOf, PALETTES, palettesFor } from "./cast";
import { SPECIES } from "./species";
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

/**
 * Every colour that is ever drawn against the wallpaper rather than against the pet.
 *
 * ⚠ EVERY CREATURE'S, not just the Proc's. Each species brings its own six colours now,
 * and a sweep that only enumerated `ALL_LOOKS` would be measuring one sixth of what is
 * actually drawn on a desktop — five bodies would ship having never been held to the
 * floor this whole palette exists to guarantee.
 */
function exposedColours(): Array<{ what: string; colour: string }> {
	const everyLook = [...ALL_LOOKS, ...SPECIES.flatMap((species) => looksOf(species.id))];
	const fromCast = everyLook.flatMap((member) => [
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
		expect(contrastRatio(PALETTES[0].body, PALETTES[0].body)).toBeCloseTo(1, 5);
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
		for (const member of [...ALL_LOOKS, ...SPECIES.flatMap((species) => looksOf(species.id))]) {
			expect(contrastRatio(PROCS_INK, member.body), member.name).toBeGreaterThanOrEqual(4.5);
			expect(contrastRatio(PROCS_INK, member.shade), member.name).toBeGreaterThanOrEqual(4.5);
		}
	});

	it("keeps each character's blush visible on its own head without shouting", () => {
		for (const member of [...ALL_LOOKS, ...SPECIES.flatMap((species) => looksOf(species.id))]) {
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
	it("gives no two colours near-identical head tints, within any creature's set", () => {
		// Six tints that measure the same are six of the same character as far as a glance
		// across the room is concerned. WITHIN a creature's own set, because two different
		// creatures being a similar colour is fine — the silhouette tells them apart, which
		// is the entire point of there being more than one body.
		// Judged on the BODY or the SHADE, whichever separates them — because a creature's
		// six are a family. A ghost is white six times over and carries its colour in the
		// fold it drapes into; demanding the cloth itself differ would be demanding six
		// ghosts that are not white. What must never happen is two entries a person cannot
		// tell apart AT ALL, and either channel doing it is enough.
		for (const species of SPECIES) {
			const set = palettesFor(species.id);
			for (const a of set) {
				for (const b of set) {
					if (a.id === b.id) continue;
					const apart = Math.max(distance(a.body, b.body), distance(a.shade, b.shade));

					expect(apart, `${species.id}: ${a.name} vs ${b.name}`).toBeGreaterThan(34);
				}
			}
		}
	});

	it("gives each creature a palette of its OWN, not a copy of the Proc's", () => {
		// The complaint this answers: one colour template wearing six shapes. A creature
		// whose six were the Proc's six would be exactly that again.
		for (const species of SPECIES.filter((entry) => entry.id !== "proc")) {
			const set = palettesFor(species.id);

			expect(set.length, species.id).toBe(PALETTES.length);
			expect(
				set.map((palette) => palette.body),
				species.id,
			).not.toEqual(PALETTES.map((palette) => palette.body));
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
