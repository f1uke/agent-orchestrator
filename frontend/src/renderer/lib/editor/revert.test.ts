import { describe, expect, it } from "vitest";
import type { Hunk } from "./change-lanes";
import { revertEdit } from "./revert";
import { branchMarks, uncommittedMarks, BRANCH_LANE_CLASS } from "./gutter-lanes";

/**
 * A stand-in model: enough of Monaco's line API to apply an edit and read the
 * result back, so the trailing-newline rules are asserted against real text
 * rather than against coordinates.
 */
function model(text: string) {
	const lines = text.split("\n");
	return {
		lineCount: lines.length,
		lastColumn: (line: number) => (lines[line - 1] ?? "").length + 1,
		apply(edit: ReturnType<typeof revertEdit>) {
			const offset = (line: number, column: number) => {
				let at = 0;
				for (let i = 1; i < line; i++) at += lines[i - 1].length + 1;
				return at + column - 1;
			};
			const from = offset(edit.startLine, edit.startColumn);
			const to = offset(edit.endLine, edit.endColumn);
			return text.slice(0, from) + edit.text + text.slice(to);
		},
	};
}

function revertOn(text: string, hunk: Hunk): string {
	const m = model(text);
	return m.apply(revertEdit(hunk, m.lineCount, m.lastColumn));
}

describe("revertEdit", () => {
	it("restores a modified run mid-file", () => {
		const out = revertOn("a\nCHANGED\nc\n", { start: 2, end: 2, kind: "modified", oldText: ["b"] });

		expect(out).toBe("a\nb\nc\n");
	});

	it("restores a multi-line modified run", () => {
		const out = revertOn("a\nX\nY\nd\n", { start: 2, end: 3, kind: "modified", oldText: ["b", "c"] });

		expect(out).toBe("a\nb\nc\nd\n");
	});

	// 🗝 The trap. Replacing "line 2..2" with "" would leave an EMPTY line 2, and
	// the file would still be one blank line away from HEAD.
	it("takes the newline with an added line, leaving no blank behind", () => {
		const out = revertOn("a\nADDED\nc\n", { start: 2, end: 2, kind: "added", oldText: [] });

		expect(out).toBe("a\nc\n");
	});

	// The other half of the same trap: at end of file there is no following
	// newline to consume, so the PRECEDING one has to go instead.
	it("takes the preceding newline for an addition at end of file", () => {
		const out = revertOn("a\nb\nADDED", { start: 3, end: 3, kind: "added", oldText: [] });

		expect(out).toBe("a\nb");
	});

	it("restores an addition at line 1", () => {
		const out = revertOn("ADDED\na\nb\n", { start: 1, end: 1, kind: "added", oldText: [] });

		expect(out).toBe("a\nb\n");
	});

	it("puts a deleted run back before the line it used to precede", () => {
		const out = revertOn("a\nc\n", { start: 2, end: 2, kind: "removed", oldText: ["b"] });

		expect(out).toBe("a\nb\nc\n");
	});

	it("appends a run deleted at end of file", () => {
		const out = revertOn("a\nb", { start: 3, end: 3, kind: "removed", oldText: ["c"] });

		expect(out).toBe("a\nb\nc");
	});

	it("restores a hunk that is the whole file", () => {
		const out = revertOn("NEW", { start: 1, end: 1, kind: "modified", oldText: ["was"] });

		expect(out).toBe("was");
	});
});

describe("gutter lanes", () => {
	it("colours the uncommitted lane by kind, one mark per covered line", () => {
		const marks = uncommittedMarks(
			[
				{ start: 2, end: 3, kind: "added" },
				{ start: 9, end: 9, kind: "removed" },
			],
			20,
		);

		expect(marks).toEqual([
			{ line: 2, className: "ao-change-bar ao-change-bar--added" },
			{ line: 3, className: "ao-change-bar ao-change-bar--added" },
			{ line: 9, className: "ao-change-bar ao-change-bar--removed" },
		]);
	});

	it("clamps a mark past the end of the buffer instead of decorating nothing", () => {
		expect(uncommittedMarks([{ start: 99, end: 99, kind: "added" }], 4)).toEqual([
			{ line: 4, className: "ao-change-bar ao-change-bar--added" },
		]);
	});

	// 🗝 One class for the whole branch lane. Two kind-coloured bars side by side
	// read as one thick bar on a branch under review.
	it("gives every branch-lane line the same neutral class", () => {
		const marks = branchMarks([2, 3, 9], 20);

		expect(new Set(marks.map((m) => m.className))).toEqual(new Set([BRANCH_LANE_CLASS]));
		expect(BRANCH_LANE_CLASS).not.toMatch(/added|modified|removed/);
	});

	it("does not decorate the same branch line twice", () => {
		expect(branchMarks([5, 5, 5], 20)).toEqual([{ line: 5, className: BRANCH_LANE_CLASS }]);
	});
});
