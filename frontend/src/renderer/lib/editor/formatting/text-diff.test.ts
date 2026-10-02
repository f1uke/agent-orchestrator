import { describe, expect, it } from "vitest";
import { applyEdits, minimalEdits } from "./text-diff";

describe("minimalEdits", () => {
	it("is empty for identical texts", () => {
		expect(minimalEdits("a\nb\n", "a\nb\n")).toEqual([]);
	});

	it("touches only the characters that changed on a re-indented line", () => {
		const before = "func f() {\n  x()\n}\n";
		const after = "func f() {\n\tx()\n}\n";
		expect(minimalEdits(before, after)).toEqual([{ start: 11, end: 13, text: "\t" }]);
	});

	it("keeps scattered changes as separate edits rather than one big replacement", () => {
		const before = ["a", "b", "c", "d", "e", "f", "g"].join("\n");
		const after = ["a", "B", "c", "d", "e", "F", "g"].join("\n");
		const edits = minimalEdits(before, after);
		expect(edits).toHaveLength(2);
		expect(applyEdits(before, edits)).toBe(after);
	});

	it("handles inserted and deleted lines, and a final line without a newline", () => {
		const cases: [string, string][] = [
			["a\nb\nc", "a\nx\nb\nc"],
			["a\nb\nc", "a\nc"],
			["a\nb", "a\nb\n"],
			["", "x\n"],
			["x\n", ""],
			['import (\n\t"b"\n\t"a"\n)\n', 'import (\n\t"a"\n\t"b"\n)\n'],
		];
		for (const [before, after] of cases) expect(applyEdits(before, minimalEdits(before, after))).toBe(after);
	});

	it("always reproduces the target text (randomised)", () => {
		let seed = 7;
		const random = () => {
			seed = (seed * 1103515245 + 12345) & 0x7fffffff;
			return seed / 0x7fffffff;
		};
		const words = ["x()", "\ty()", "  z", "}", "{", "", "// c"];
		const text = (n: number) =>
			Array.from({ length: n }, () => words[Math.floor(random() * words.length)]).join("\n") +
			(random() < 0.5 ? "\n" : "");
		for (let round = 0; round < 300; round++) {
			const before = text(Math.floor(random() * 30));
			const after = text(Math.floor(random() * 30));
			expect(applyEdits(before, minimalEdits(before, after))).toBe(after);
		}
	});
});
