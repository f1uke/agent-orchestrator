import { describe, expect, it } from "vitest";
import { locateRow } from "./reveal";

const NOTE = ["# Tasks", "", "## My active items", "", "- [ ] first row", "- [ ] second row", ""].join("\n");

describe("locateRow", () => {
	it("uses the line it was given when that line still reads the same", () => {
		const found = locateRow(NOTE, 6, "- [ ] second row");
		expect(found).toEqual({
			kind: "found",
			line: 6,
			moved: false,
			span: { start: NOTE.indexOf("- [ ] second row"), end: NOTE.indexOf("- [ ] second row") + 16 },
		});
	});

	/**
	 * 🗝 The line is a hint and the text is the key — the same rule the tick
	 * follows. A row that slid down while the tab was open is still findable,
	 * and the reader is told it moved rather than being dropped on whatever
	 * happens to sit at the old number now.
	 */
	it("finds a row that moved, and says so", () => {
		const moved = ["- [ ] a new row on top", ...NOTE.split("\n")].join("\n");
		const at = locateRow(moved, 6, "- [ ] second row");
		expect(at).toMatchObject({ kind: "found", line: 7, moved: true });
	});

	it("points at the row when somebody else has already ticked it", () => {
		const ticked = NOTE.replace("- [ ] first row", "- [x] first row");
		expect(locateRow(ticked, 5, "- [ ] first row")).toMatchObject({ kind: "done", line: 5 });
	});

	it("refuses to point when two rows read exactly alike", () => {
		const twice = `${NOTE}- [ ] second row\n`;
		expect(locateRow(twice, 99, "- [ ] second row")).toEqual({ kind: "ambiguous", matches: 2 });
	});

	it("says the row is gone rather than guessing a nearby line", () => {
		expect(locateRow(NOTE, 6, "- [ ] a row that was reworded")).toEqual({ kind: "gone" });
	});

	it("keeps its offsets honest in a note with CRLF endings", () => {
		const crlf = NOTE.split("\n").join("\r\n");
		const at = locateRow(crlf, 6, "- [ ] second row");
		expect(at).toMatchObject({ kind: "found", moved: false });
		if (at.kind !== "found") throw new Error("unreachable");
		expect(crlf.slice(at.span.start, at.span.end)).toBe("- [ ] second row\r");
	});
});
