import type { Hunk } from "./change-lanes";

/** A whole-line replacement, in Monaco's 1-based inclusive line coordinates. */
export type RevertEdit = {
	startLine: number;
	startColumn: number;
	endLine: number;
	endColumn: number;
	text: string;
};

/**
 * The edit that restores one hunk to what it was at the diff's base.
 *
 * 🗝 Reverting must work in WHOLE LINES, and that is the trap this function
 * exists for. Replacing "line 96..96" with the empty string leaves an EMPTY
 * line 96 behind: the file ends up one blank line away from HEAD and `git
 * status` still calls it dirty. The range has to reach column 1 of the FOLLOWING
 * line so the newline is consumed — and at end of file, where there is no
 * following line, it has to take the PRECEDING newline instead.
 *
 * @param hunk       the run to restore, in new-side coordinates
 * @param lineCount  the model's current line count
 * @param lastColumn a function giving the model's max column for a line, so the
 *                   end-of-file case can select to the true end of the text
 */
export function revertEdit(hunk: Hunk, lineCount: number, lastColumn: (line: number) => number): RevertEdit {
	const restored = hunk.oldText.join("\n");

	// A pure deletion occupies no line: put the removed text back BEFORE the
	// line it used to precede, as a zero-width insertion.
	if (hunk.kind === "removed") {
		const at = Math.min(Math.max(hunk.start, 1), lineCount + 1);
		if (at > lineCount) {
			// Deleted at end of file: append after the last line instead.
			const end = lastColumn(lineCount);
			return { startLine: lineCount, startColumn: end, endLine: lineCount, endColumn: end, text: `\n${restored}` };
		}
		return { startLine: at, startColumn: 1, endLine: at, endColumn: 1, text: `${restored}\n` };
	}

	const start = Math.min(Math.max(hunk.start, 1), lineCount);
	const end = Math.min(Math.max(hunk.end, start), lineCount);

	// Ordinary case: select through the start of the next line, so the removed
	// lines take their own newlines with them.
	if (end < lineCount) {
		return {
			startLine: start,
			startColumn: 1,
			endLine: end + 1,
			endColumn: 1,
			text: restored === "" ? "" : `${restored}\n`,
		};
	}

	// End of file: there is no following newline to consume, so take the
	// PRECEDING one — otherwise removing the last lines leaves a blank one.
	if (start > 1) {
		return {
			startLine: start - 1,
			startColumn: lastColumn(start - 1),
			endLine: end,
			endColumn: lastColumn(end),
			text: restored === "" ? "" : `\n${restored}`,
		};
	}

	// The hunk is the whole file.
	return { startLine: 1, startColumn: 1, endLine: end, endColumn: lastColumn(end), text: restored };
}
