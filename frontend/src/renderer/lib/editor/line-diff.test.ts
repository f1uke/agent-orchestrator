import { describe, expect, it } from "vitest";
import { diffLines, type LineRun } from "./line-diff";

/** Apply runs to `a`, which must reproduce `b` exactly. */
function apply(a: readonly string[], b: readonly string[], runs: readonly LineRun[]): string[] {
	const out: string[] = [];
	let at = 0;
	for (const run of runs) {
		out.push(...a.slice(at, run.a0), ...b.slice(run.b0, run.b1));
		at = run.a1;
	}
	out.push(...a.slice(at));
	return out;
}

describe("diffLines", () => {
	it("is empty for identical inputs", () => {
		expect(diffLines(["a", "b"], ["a", "b"])).toEqual([]);
		expect(diffLines([], [])).toEqual([]);
	});

	it("reports an insertion, a deletion and a replacement as separate runs", () => {
		const a = ["a", "b", "c", "d", "e", "f"];
		const b = ["a", "NEW", "b", "c", "e", "F"];
		expect(diffLines(a, b)).toEqual([
			{ a0: 1, a1: 1, b0: 1, b1: 2 },
			{ a0: 3, a1: 4, b0: 4, b1: 4 },
			{ a0: 5, a1: 6, b0: 5, b1: 6 },
		]);
	});

	it("handles everything added and everything removed", () => {
		expect(diffLines([], ["x", "y"])).toEqual([{ a0: 0, a1: 0, b0: 0, b1: 2 }]);
		expect(diffLines(["x", "y"], [])).toEqual([{ a0: 0, a1: 2, b0: 0, b1: 0 }]);
	});

	it("falls back to one run over the changed middle when the texts are too far apart", () => {
		const a = ["head", "1", "2", "3", "4", "tail"];
		const b = ["head", "w", "x", "y", "z", "tail"];
		expect(diffLines(a, b, 2)).toEqual([{ a0: 1, a1: 5, b0: 1, b1: 5 }]);
	});

	it("always reproduces the target (randomised)", () => {
		let seed = 11;
		const random = () => {
			seed = (seed * 1103515245 + 12345) & 0x7fffffff;
			return seed / 0x7fffffff;
		};
		const words = ["a", "b", "c", "d", "", "}"];
		for (let round = 0; round < 400; round++) {
			const a = Array.from({ length: Math.floor(random() * 30) }, () => words[Math.floor(random() * words.length)]);
			const b = a.filter(() => random() > 0.2);
			for (let i = 0; i < 5; i++) b.splice(Math.floor(random() * (b.length + 1)), 0, words[Math.floor(random() * words.length)]);
			const runs = diffLines(a, b);
			expect(apply(a, b, runs)).toEqual(b);
			// Runs are ordered and never touch: adjacent edits fold into one.
			for (let i = 1; i < runs.length; i++) {
				expect(runs[i].a0).toBeGreaterThan(runs[i - 1].a1 - 1);
				expect(runs[i].a0 > runs[i - 1].a1 || runs[i].b0 > runs[i - 1].b1).toBe(true);
			}
		}
	});

	it("stays fast on a large file with a scattered few edits", () => {
		const a = Array.from({ length: 50_000 }, (_, i) => `line ${i}`);
		const b = [...a];
		for (const at of [10, 20_000, 49_990]) b[at] = "edited";
		const started = performance.now();
		expect(diffLines(a, b)).toHaveLength(3);
		expect(performance.now() - started).toBeLessThan(500);
	});
});
