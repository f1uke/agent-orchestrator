import { describe, expect, it } from "vitest";
import type { Hunk } from "./live-changes";
import { revertEdit } from "./revert";

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
	// The joined old text of one blank line is "", which used to read as
	// "nothing to restore" and deleted the line instead of blanking it.
	it("restores a line that was blank before it was typed on", () => {
		expect(revertOn("a\ntyped\nc", { start: 2, end: 2, kind: "modified", oldText: [""] })).toBe("a\n\nc");
		expect(revertOn("a\ntyped", { start: 2, end: 2, kind: "modified", oldText: [""] })).toBe("a\n");
	});

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
