import { describe, expect, it } from "vitest";
import { laneMarks } from "./gutter-lanes";
import type { Hunk } from "./live-changes";

const hunk = (start: number, end: number, kind: Hunk["kind"]): Hunk => ({ start, end, kind, oldText: [] });
const none = { branch: [], uncommitted: [], unsaved: [] };

describe("laneMarks", () => {
	it("colours BOTH git lanes by kind, with lane-scoped classes", () => {
		const marks = laneMarks({ ...none, branch: [hunk(2, 3, "added")], uncommitted: [hunk(3, 3, "modified")] }, 20);
		expect(marks).toEqual([
			{
				lane: "branch",
				kind: "added",
				start: 2,
				end: 3,
				glyphClassName: "ao-gutter-lane ao-branch-bar ao-branch-bar--added",
			},
			{
				lane: "uncommitted",
				kind: "modified",
				start: 3,
				end: 3,
				glyphClassName: "ao-gutter-lane ao-change-bar ao-change-bar--modified",
			},
		]);
	});

	// 🗝 Both git lanes share one node per line, so a kind class shared between
	// them would paint the other lane's bar too.
	it("never lets a kind class name be shared between the two git lanes", () => {
		const marks = laneMarks({ ...none, branch: [hunk(1, 1, "added")], uncommitted: [hunk(1, 1, "added")] }, 5);
		const [branch, uncommitted] = marks.map((m) => new Set(m.glyphClassName?.split(" ")));
		const shared = [...branch].filter((c) => uncommitted.has(c));
		expect(shared).toEqual(["ao-gutter-lane"]);
	});

	it("marks unsaved lines on the line number, not in the glyph margin", () => {
		const marks = laneMarks({ ...none, unsaved: [hunk(4, 5, "modified"), hunk(8, 8, "removed")] }, 20);
		expect(marks.map((m) => [m.start, m.end, m.lineNumberClassName, m.glyphClassName])).toEqual([
			[4, 5, "ao-unsaved-line", undefined],
			[8, 8, "ao-unsaved-removed", undefined],
		]);
	});

	it("moves a removal past the last line onto the last line's bottom edge", () => {
		const marks = laneMarks({ branch: [], uncommitted: [hunk(5, 5, "removed")], unsaved: [hunk(5, 5, "removed")] }, 4);
		expect(marks.map((m) => [m.start, m.glyphClassName ?? m.lineNumberClassName])).toEqual([
			[4, "ao-gutter-lane ao-change-bar ao-change-bar--removed ao-change-bar--end"],
			[4, "ao-unsaved-removed ao-unsaved-removed--end"],
		]);
	});

	it("clamps a run that reaches past the buffer instead of decorating nothing", () => {
		expect(laneMarks({ ...none, uncommitted: [hunk(3, 99, "added")] }, 4)[0]).toMatchObject({ start: 3, end: 4 });
	});
});
