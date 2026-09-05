/**
 * Where a row the reader clicked in the Tasks tab actually is, in the note as
 * it stands NOW.
 *
 * 🗝 A line number read minutes ago is a HINT, never an address. The vault's
 * own agent edits these notes constantly, so by the time the reader clicks the
 * source line the row may have moved, been ticked off elsewhere, been reworded
 * or been deleted. Scrolling to line N regardless would land the reader on
 * somebody else's row and tell them it was theirs, which is worse than not
 * scrolling at all.
 *
 * So this follows exactly the rule the TICK follows (`service/wiki
 * tasks_complete.go: findTaskLine`): the row's own text, byte for byte, is the
 * key; the line is only tried first. Every outcome the daemon distinguishes is
 * distinguished here too, so the reader is told which one happened rather than
 * being dropped somewhere and left to work it out.
 */

/** A half-open byte range in the note's content. */
export type LineSpan = { start: number; end: number };

export type RowLocation =
	/**
	 * The row is there. `moved` says it was not where the list said it was —
	 * the text still matched exactly, so it is provably the same row.
	 */
	| { kind: "found"; span: LineSpan; line: number; moved: boolean }
	/** The same row, with its box already filled in by somebody else. */
	| { kind: "done"; span: LineSpan; line: number }
	/** Not in the note with the text it was shown with. Nowhere to point. */
	| { kind: "gone" }
	/** Several rows read exactly alike, so there is no way to tell which. */
	| { kind: "ambiguous"; matches: number };

/**
 * Locates one task row in a note.
 *
 * `line` is 1-based, as the daemon returns it; `raw` is the line byte for byte
 * as the row was drawn with it.
 */
export function locateRow(content: string, line: number, raw: string): RowLocation {
	const want = trimCR(raw);
	if (want === "") return { kind: "gone" };

	const lines = content.split("\n");
	const starts = lineStarts(lines);
	const at = line - 1;

	if (at >= 0 && at < lines.length && trimCR(lines[at]) === want) {
		return { kind: "found", span: spanOf(starts, lines, at), line, moved: false };
	}

	const matches: number[] = [];
	for (let i = 0; i < lines.length; i += 1) if (trimCR(lines[i]) === want) matches.push(i);
	if (matches.length === 1) {
		return { kind: "found", span: spanOf(starts, lines, matches[0]), line: matches[0] + 1, moved: true };
	}
	if (matches.length > 1) return { kind: "ambiguous", matches: matches.length };

	// Nothing reads like the row any more. Before saying "gone", look for the
	// one shape that means something better than that: the very same row with
	// its box ticked, which is what the vault's own agent writes when it closes
	// a row out. Pointing at it answers the reader's question.
	const done = tick(want);
	if (done !== null) {
		const ticked =
			at >= 0 && at < lines.length && trimCR(lines[at]) === done ? at : lines.findIndex((l) => trimCR(l) === done);
		if (ticked >= 0) return { kind: "done", span: spanOf(starts, lines, ticked), line: ticked + 1 };
	}
	return { kind: "gone" };
}

/** `- [ ] a row` → `- [x] a row`, changing that one byte and nothing else. */
const TASK_ROW = /^(\s*(?:[-*+]|\d+[.)])\s+\[)([ xX])(\])/;

function tick(line: string): string | null {
	const match = TASK_ROW.exec(line);
	if (!match || match[2] !== " ") return null;
	const box = match[1].length;
	return `${line.slice(0, box)}x${line.slice(box + 1)}`;
}

/** A note's line endings survive: only the comparison drops the CR. */
function trimCR(line: string): string {
	return line.endsWith("\r") ? line.slice(0, -1) : line;
}

function lineStarts(lines: string[]): number[] {
	const starts: number[] = [];
	let offset = 0;
	for (const line of lines) {
		starts.push(offset);
		offset += line.length + 1; // the "\n" the split consumed
	}
	return starts;
}

function spanOf(starts: number[], lines: string[], index: number): LineSpan {
	return { start: starts[index], end: starts[index] + lines[index].length };
}
