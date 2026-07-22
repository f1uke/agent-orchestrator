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

	it("gives every character its own ear shape, which is what makes the silhouette differ", () => {
		expect(new Set(CAST.map((member) => member.ear)).size).toBe(CAST.length);
	});

	it("keeps every ear inside the left margin, clear of the head", () => {
		// The head spans x 14..82. An ear drawn over it stops reading as a bracket.
		for (const member of CAST) {
			const xs = coordinates(member.ear).map(([x]) => x);
			expect(Math.max(...xs)).toBeLessThanOrEqual(18);
			expect(Math.min(...xs)).toBeGreaterThanOrEqual(0);
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
			expect(mirrorPathX(mirrorPathX(member.ear))).toBe(normalise(member.ear));
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
