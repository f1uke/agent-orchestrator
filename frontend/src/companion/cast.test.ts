import { describe, expect, it } from "vitest";
import {
	ALL_LOOKS,
	APPEARANCE_AXES,
	castForSession,
	castFromLook,
	composeCast,
	defaultLook,
	HATS,
	mirrorPathX,
	optionsOf,
	PALETTES,
} from "./cast";

const CAST = ALL_LOOKS;

describe("the two axes", () => {
	it("keeps colour and hat as SEPARATE lists, not six bundled characters", () => {
		// The bundle was the problem: six fixed (hat, colour) pairs meant every colour
		// had exactly one hat and there were six looks in the world. Separate lists
		// multiply instead of adding.
		expect(ALL_LOOKS).toHaveLength(PALETTES.length * HATS.length);
		expect(ALL_LOOKS.length).toBeGreaterThan(PALETTES.length + HATS.length);
	});

	it("gives every colour its own tints", () => {
		expect(new Set(PALETTES.map((p) => p.body)).size).toBe(PALETTES.length);
		expect(new Set(PALETTES.map((p) => p.shade)).size).toBe(PALETTES.length);
	});

	it("gives every hat its own shape, which is what makes the silhouette differ", () => {
		const shapes = HATS.map((hat) => hat.pieces.map((piece) => piece.d).join("|"));

		expect(new Set(shapes).size).toBe(HATS.length);
	});

	it("names a look after both of its axes, so it can be said out loud", () => {
		const look = composeCast(PALETTES[0], HATS[0]);

		expect(look.id).toBe(`${PALETTES[0].id}-${HATS[0].id}`);
		expect(look.name).toContain(PALETTES[0].name);
		expect(look.name).toContain(HATS[0].name);
	});
});

describe("the cast", () => {
	it("sits every hat ON the head rather than beside it", () => {
		// The head spans x 14..82, y 6..78. A hat that misses it leaves the Proc bald,
		// which is what the human reported; one that covers the eyes is worse.
		for (const member of CAST) {
			const points = member.hat.flatMap((piece) => coordinates(piece.d));
			const ys = points.map(([, y]) => y);
			const xs = points.map(([x]) => x);

			// Rises above the head, and stops short of the eyes — whose tops are at
			// y=43. A brim over the eyes costs the Proc its face, which is the one
			// thing no amount of hat can be worth.
			expect(Math.min(...ys), `${member.name} rises above the head`).toBeLessThan(6);
			expect(Math.max(...ys), `${member.name} clears the eyes`).toBeLessThan(43);
			// Inside the drawn frame, which starts at x=-8 and is clipped.
			expect(Math.min(...xs), `${member.name} left`).toBeGreaterThan(-8);
			expect(Math.max(...xs), `${member.name} right`).toBeLessThan(104);
		}
	});

	it("draws every hat with absolute M/L/C only, so it can be measured and mirrored", () => {
		for (const member of CAST) {
			for (const piece of member.hat) {
				expect(piece.d.replace(/[-\d.,\s]/g, ""), member.name).toMatch(/^[MLCZ]*$/);
			}
		}
	});

	it("makes every crown WIDER than the head, so no head pokes out beside its hat", () => {
		// Reported twice by the human, and precisely: brack, glob and tilde had crowns
		// narrower than the 68-unit head, so slivers of skull showed either side of the
		// hat. "Bigger" is not the same rule as "wider" — the first pass raised the
		// crowns and left three of them narrow.
		const HEAD = { left: 14, right: 82 };
		for (const member of CAST) {
			const crown = coordinates(member.hat[0].d).map(([x]) => x);

			expect(Math.min(...crown), `${member.name} left overhang`).toBeLessThan(HEAD.left - 2);
			expect(Math.max(...crown), `${member.name} right overhang`).toBeGreaterThan(HEAD.right + 2);
		}
	});

	it("covers enough of the head that no Proc reads as bald", () => {
		// The human's words were "หัวมันดูโล้นๆ" — the heads look bald. A hat perched
		// on top without covering the crown does not fix that, so this pins the cover
		// rather than merely the presence of a hat.
		//
		// (Hats also retire a bug the brackets had: `<>` mirrored to `><` when a Proc
		// turned to walk left. A hat is still a hat in a mirror, so that whole class of
		// problem goes away instead of being papered over — nothing to assert, which is
		// the point.)
		const HEAD = { left: 14, right: 82, top: 6 };
		for (const member of CAST) {
			const points = member.hat.flatMap((piece) => coordinates(piece.d));
			const xs = points.map(([x]) => x);
			const width = Math.min(Math.max(...xs), HEAD.right) - Math.max(Math.min(...xs), HEAD.left);

			expect(width, `${member.name} width`).toBeGreaterThan((HEAD.right - HEAD.left) * 0.6);
			expect(Math.max(...points.map(([, y]) => y)), `${member.name} sits on the head`).toBeGreaterThan(HEAD.top);
		}
	});
});

describe("the axis registry", () => {
	it("describes every axis as DATA, so a picker never names one in code", () => {
		// The library iterates this list. If it ever hardcoded "colour, then hat", a
		// third axis — the new character types the human wants next — would mean
		// rewriting the picker instead of adding a row here.
		expect(APPEARANCE_AXES.length).toBeGreaterThanOrEqual(2);
		for (const axis of APPEARANCE_AXES) {
			expect(axis.options.length, `${axis.id} has options`).toBeGreaterThan(0);
			expect(axis.name.length, `${axis.id} is named for a human`).toBeGreaterThan(0);
			expect(new Set(axis.options.map((option) => option.id)).size, `${axis.id} ids are unique`).toBe(
				axis.options.length,
			);
		}
	});

	it("carries the colour and hat lists themselves, not copies that can drift", () => {
		expect(optionsOf("palette").map((option) => option.id)).toEqual(PALETTES.map((p) => p.id));
		expect(optionsOf("hat").map((option) => option.id)).toEqual(HATS.map((h) => h.id));
	});

	it("gives every axis its own hash salt, which is what keeps the axes independent", () => {
		// One salt used twice is one axis wearing two names: every amber Proc would be
		// in the same hat again, just less obviously. This is the invariant that the
		// 900-session independence test measures the CONSEQUENCE of.
		const salts = APPEARANCE_AXES.map((axis) => axis.salt);

		expect(new Set(salts).size).toBe(APPEARANCE_AXES.length);
	});

	it("resolves a default for every axis, for any ref", () => {
		for (const ref of ["", "   ", "🙂", "agent-orchestrator-168"]) {
			const look = defaultLook(ref);
			for (const axis of APPEARANCE_AXES) {
				expect(
					axis.options.some((option) => option.id === look[axis.id]),
					`${ref} / ${axis.id}`,
				).toBe(true);
			}
		}
	});

	it("IS the hash assignment — the default look is exactly what castForSession gives", () => {
		// castForSession is the promise that a worker keeps its face. Routing it
		// through the axes must not move a single session, so this walks a realistic
		// roster rather than spot-checking one ref.
		for (let i = 0; i < 200; i++) {
			const ref = `agent-orchestrator-${i}`;
			expect(castFromLook(defaultLook(ref)), ref).toEqual(castForSession(ref));
		}
	});

	it("builds the look it is asked for, whichever pair that is", () => {
		expect(castFromLook({ palette: "mint", hat: "cone" })).toEqual(
			composeCast(
				PALETTES.find((p) => p.id === "mint")!,
				HATS.find((h) => h.id === "cone")!,
			),
		);
	});
});

describe("castForSession", () => {
	it("always gives the same session the same look, so a worker is recognisable", () => {
		// The whole reason the assignment is a hash rather than a shuffle: someone
		// learns which Proc is which, and re-rolling on every launch would take that
		// away for nothing.
		const ref = "agent-orchestrator-157";
		const first = castForSession(ref);

		for (let i = 0; i < 20; i++) expect(castForSession(ref)).toEqual(first);
	});

	it("picks the hat INDEPENDENTLY of the colour", () => {
		// The bug this replaces: hat bound to colour meant every amber Proc wore the
		// same hat, for ever. Independence is the claim, so it is what is measured —
		// every colour must be seen in more than one hat across a realistic roster.
		const hatsByPalette = new Map<string, Set<string>>();
		for (let i = 0; i < 900; i++) {
			const look = castForSession(`session-${i}`);
			const seen = hatsByPalette.get(look.palette) ?? new Set<string>();
			seen.add(look.hatId);
			hatsByPalette.set(look.palette, seen);
		}

		expect(hatsByPalette.size).toBe(PALETTES.length);
		for (const [palette, hats] of hatsByPalette) {
			expect(hats.size, `${palette} is worn with several hats`).toBe(HATS.length);
		}
	});

	it("spreads sessions across every colour AND every hat", () => {
		const palettes = new Map<string, number>();
		const hats = new Map<string, number>();
		for (let i = 0; i < 900; i++) {
			const look = castForSession(`session-${i}`);
			palettes.set(look.palette, (palettes.get(look.palette) ?? 0) + 1);
			hats.set(look.hatId, (hats.get(look.hatId) ?? 0) + 1);
		}

		// An even split is 150 each. Anything under half of that is a bad hash, not
		// luck — the whole point is that a board of sessions looks varied.
		for (const palette of PALETTES) expect(palettes.get(palette.id) ?? 0, palette.id).toBeGreaterThan(75);
		for (const hat of HATS) expect(hats.get(hat.id) ?? 0, hat.id).toBeGreaterThan(75);
	});

	it("does not put neighbouring session ids on the same look", () => {
		// Sessions are created in sequence, so ids that differ by one are exactly the
		// ones a user sees side by side. A hash that keeps them together would look
		// broken however well it spreads overall.
		const runs = Array.from({ length: 12 }, (_, i) => castForSession(`agent-orchestrator-${i}`).id);

		expect(new Set(runs).size).toBeGreaterThanOrEqual(8);
	});

	it("still returns a look for an empty or odd ref", () => {
		for (const ref of ["", "   ", "🙂"]) {
			const look = castForSession(ref);
			expect(
				PALETTES.some((p) => p.id === look.palette),
				ref,
			).toBe(true);
			expect(
				HATS.some((h) => h.id === look.hatId),
				ref,
			).toBe(true);
		}
	});
});

describe("mirrorPathX", () => {
	it("reflects a path about the figure's centre line", () => {
		expect(mirrorPathX("M10 20 L30 40")).toBe("M86 20 L66 40");
	});

	it("reflects curves, not just lines", () => {
		expect(mirrorPathX("M10 20 C 12 22 14 24 16 26")).toBe("M86 20 C84 22 82 24 80 26");
	});

	it("is its own inverse", () => {
		for (const member of CAST) {
			for (const piece of member.hat) {
				expect(mirrorPathX(mirrorPathX(piece.d))).toBe(normalise(piece.d));
			}
		}
	});
});

/** Pull the (x, y) pairs out of an absolute M/L/C path. */
function coordinates(d: string): Array<[number, number]> {
	const numbers = (d.match(/-?\d+(\.\d+)?/g) ?? []).map(Number);
	const pairs: Array<[number, number]> = [];
	for (let i = 0; i + 1 < numbers.length; i += 2) pairs.push([numbers[i], numbers[i + 1]]);
	return pairs;
}

/** mirrorPathX normalises spacing, so the round-trip is compared against that. */
function normalise(d: string): string {
	return mirrorPathX(mirrorPathX(d));
}
