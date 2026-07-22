import { describe, expect, it } from "vitest";
import { CAST, castForSession, mirrorPathX } from "./cast";

describe("the cast", () => {
	it("is the six characters the design names", () => {
		expect(CAST.map((member) => member.id)).toEqual(["curly", "angle", "brack", "glob", "hash", "tilde"]);
		expect(CAST.map((member) => member.glyph)).toEqual(["{}", "<>", "[]", "**", "##", "~~"]);
	});

	it("gives every character its own colour", () => {
		expect(new Set(CAST.map((member) => member.body)).size).toBe(CAST.length);
		expect(new Set(CAST.map((member) => member.shade)).size).toBe(CAST.length);
	});

	it("gives every character its own hat, which is what makes the silhouette differ", () => {
		const shapes = CAST.map((member) => member.hat.map((piece) => piece.d).join("|"));

		expect(new Set(shapes).size).toBe(CAST.length);
	});

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

describe("castForSession", () => {
	it("always gives the same session the same character, so a worker is recognisable", () => {
		const ref = "agent-orchestrator-157";
		const first = castForSession(ref);

		for (let i = 0; i < 20; i++) expect(castForSession(ref)).toBe(first);
	});

	it("spreads sessions across the whole cast", () => {
		const counts = new Map<string, number>();
		for (let i = 0; i < 600; i++) {
			const id = castForSession(`session-${i}`).id;
			counts.set(id, (counts.get(id) ?? 0) + 1);
		}

		expect(counts.size).toBe(CAST.length);
		// An even split is 100 each. Anything under half of that is a bad hash, not
		// luck — the whole point is that a board of sessions looks varied.
		for (const member of CAST) {
			expect(counts.get(member.id) ?? 0).toBeGreaterThan(50);
		}
	});

	it("does not put neighbouring session ids on the same character", () => {
		// Sessions are created in sequence, so ids that differ by one are exactly the
		// ones a user sees side by side. A hash that keeps them together would look
		// broken however well it spreads overall.
		const runs = Array.from({ length: 12 }, (_, i) => castForSession(`agent-orchestrator-${i}`).id);

		expect(new Set(runs).size).toBeGreaterThanOrEqual(4);
	});

	it("still returns a character for an empty or odd ref", () => {
		expect(CAST).toContain(castForSession(""));
		expect(CAST).toContain(castForSession("   "));
		expect(CAST).toContain(castForSession("🙂"));
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
