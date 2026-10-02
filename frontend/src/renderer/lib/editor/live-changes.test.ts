import { describe, expect, it } from "vitest";
import { hunksBetween, linesOf, liveLanes, markedLineCount, modelLines } from "./live-changes";
import { revertEdit } from "./revert";

describe("linesOf", () => {
	it("reads a file the way the model holds it", () => {
		expect(linesOf("a\nb\n")).toEqual(["a", "b"]);
		expect(linesOf("a\nb")).toEqual(["a", "b"]);
		expect(linesOf("a\r\nb\r\n")).toEqual(["a", "b"]);
		expect(linesOf("a\n\n")).toEqual(["a", ""]);
	});

	it("treats an empty file and a lone empty line as the same zero lines", () => {
		expect(linesOf("")).toEqual([]);
		expect(linesOf("\n")).toEqual([]);
		expect(modelLines([""])).toEqual([]);
		expect(modelLines(["", ""])).toEqual(["", ""]);
	});
});

describe("hunksBetween", () => {
	it("classifies the way the git hunks are classified", () => {
		const base = ["a", "b", "c", "d"];
		const current = ["a", "B", "c", "new", "d"];
		expect(hunksBetween(base, current)).toEqual([
			{ start: 2, end: 2, kind: "modified", oldText: ["b"] },
			{ start: 4, end: 4, kind: "added", oldText: [] },
		]);
	});

	it("puts a removal on the line it used to precede, and one past the end at end of file", () => {
		expect(hunksBetween(["a", "gone", "b"], ["a", "b"])).toEqual([
			{ start: 2, end: 2, kind: "removed", oldText: ["gone"] },
		]);
		expect(hunksBetween(["a", "gone"], ["a"])).toEqual([{ start: 2, end: 2, kind: "removed", oldText: ["gone"] }]);
	});

	// The contract Discard Change rests on: every hunk, reverted, puts back
	// exactly the base. Applied bottom-up so earlier coordinates stay valid.
	it("round-trips through revertEdit back to the base (randomised)", () => {
		let seed = 3;
		const random = () => {
			seed = (seed * 1103515245 + 12345) & 0x7fffffff;
			return seed / 0x7fffffff;
		};
		const words = ["x", "y", "z", "", "{", "}"];
		for (let round = 0; round < 300; round++) {
			const base = Array.from({ length: 1 + Math.floor(random() * 15) }, () => words[Math.floor(random() * 6)]);
			const current = base.filter(() => random() > 0.25).map((w) => (random() > 0.85 ? `${w}!` : w));
			if (random() > 0.5) current.splice(Math.floor(random() * (current.length + 1)), 0, "ins");
			if (current.length === 0) current.push("only");
			let text = [...current];
			for (const hunk of [...hunksBetween(base, current)].reverse()) {
				const edit = revertEdit(hunk, text.length, (line) => text[line - 1].length + 1);
				text = applyEdit(text, edit);
			}
			expect(text).toEqual(base);
		}
	});
});

function applyEdit(
	lines: string[],
	edit: { startLine: number; startColumn: number; endLine: number; endColumn: number; text: string },
): string[] {
	const joined = lines.join("\n");
	const offset = (line: number, column: number) => {
		let at = 0;
		for (let i = 0; i < line - 1; i++) at += lines[i].length + 1;
		return at + column - 1;
	};
	const next =
		joined.slice(0, offset(edit.startLine, edit.startColumn)) +
		edit.text +
		joined.slice(offset(edit.endLine, edit.endColumn));
	return next.split("\n");
}

describe("liveLanes", () => {
	const head = ["l1", "l2", "l3", "l4"];
	const target = ["l1", "l3"];

	it("measures every lane against the buffer, so an unsaved line is marked at once", () => {
		const saved = ["l1", "l2", "l3", "l4"];
		const current = ["l1", "l2", "typed", "l3", "l4"];
		const lanes = liveLanes({ current, saved, head, target, diskUncommitted: [] });
		expect(lanes.unsaved).toEqual([{ start: 3, end: 3, kind: "added", oldText: [] }]);
		expect(lanes.uncommitted).toEqual([{ start: 3, end: 3, kind: "added", oldText: [] }]);
		expect(lanes.branch.map((h) => [h.start, h.end, h.kind])).toEqual([
			[2, 3, "added"],
			[5, 5, "added"],
		]);
		expect(lanes.discardable).toBe(lanes.uncommitted);
	});

	it("clears the unsaved lane when the buffer is back to what was saved", () => {
		const lanes = liveLanes({ current: head, saved: head, head, target, diskUncommitted: [] });
		expect(lanes.unsaved).toEqual([]);
		expect(lanes.uncommitted).toEqual([]);
	});

	it("without HEAD's text, carries the disk map through the unsaved edits and offers nothing to discard", () => {
		const saved = ["a", "b", "c", "d", "e"];
		// Disk: b..c modified, a removal above e.
		const diskUncommitted = [
			{ start: 2, end: 3, kind: "modified" },
			{ start: 5, end: 5, kind: "removed" },
		];
		// Two lines inserted at the top, and c itself edited.
		const current = ["new1", "new2", "a", "b", "C", "d", "e"];
		const lanes = liveLanes({ current, saved, head: null, target: null, diskUncommitted });
		expect(lanes.uncommitted).toEqual([
			{ start: 4, end: 4, kind: "modified", oldText: [] },
			{ start: 7, end: 7, kind: "removed", oldText: [] },
		]);
		expect(lanes.branch).toEqual([]);
		expect(lanes.discardable).toEqual([]);
	});

	it("counts marked lines the way the header always has", () => {
		expect(
			markedLineCount([
				{ start: 1, end: 3, kind: "modified", oldText: [] },
				{ start: 5, end: 5, kind: "removed", oldText: ["x"] },
			]),
		).toBe(4);
	});
});
