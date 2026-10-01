import type { InfillRequest } from "../../../main/inline-completion/infill";

/**
 * Building one fill-in-the-middle request from a buffer, and cleaning up what
 * comes back. Pure functions over strings, so the rules are tested directly
 * rather than through Monaco.
 *
 * The shape is llama.vim's, which is what llama-server's `/infill` was designed
 * against: the cursor's line is split into `prompt` (before the cursor, placed
 * AFTER the FIM middle token so the model continues it) and the start of
 * `inputSuffix`; whole lines above and below make up the rest.
 */

/** Lines above the cursor sent as prefix. The server keeps the last 3/4 batch of tokens of it. */
export const PREFIX_LINES = 128;
/** Lines below the cursor sent as suffix. The server keeps 1/4 batch of tokens of it. */
export const SUFFIX_LINES = 64;
/**
 * More than this many non-blank characters right of the cursor and no request is
 * made (llama.vim's `max_line_suffix`). Typing in the middle of an existing line
 * is editing, not writing, and a guess there is noise.
 */
export const MAX_LINE_SUFFIX = 8;

export type BufferView = {
	/** Every line of the buffer, without terminators. */
	lines: readonly string[];
	/** 1-based, as Monaco reports it. */
	lineNumber: number;
	/** 1-based, as Monaco reports it. */
	column: number;
};

/** Null when this position should not be asked about at all. */
export function buildInfillRequest(view: BufferView, inputExtra: InfillRequest["inputExtra"]): InfillRequest | null {
	const index = view.lineNumber - 1;
	const line = view.lines[index];
	if (line === undefined) return null;
	const before = line.slice(0, view.column - 1);
	const after = line.slice(view.column - 1);
	if (after.trim().length > MAX_LINE_SUFFIX) return null;
	const above = view.lines.slice(Math.max(0, index - PREFIX_LINES), index);
	const below = view.lines.slice(index + 1, index + 1 + SUFFIX_LINES);
	return {
		inputPrefix: above.length > 0 ? `${above.join("\n")}\n` : "",
		prompt: before,
		inputSuffix: `${after}\n${below.join("\n")}`,
		inputExtra,
		nIndent: line.length - line.trimStart().length,
	};
}

/**
 * Turn what the model produced into what is worth showing, or null.
 *
 * Three things a FIM model routinely does that a person should never see:
 * - finish with blank or whitespace-only lines;
 * - produce nothing but whitespace (`n_indent` stopped it on the first line);
 * - write the code that is ALREADY below the cursor - the closing brace, the
 *   next statement - because the suffix ended where it did. Lines at the end of
 *   the guess that repeat the lines right after the cursor are dropped, and a
 *   guess that is only that repetition is dropped whole.
 */
export function cleanCompletion(content: string, view: BufferView): string | null {
	let lines = content.replace(/\r\n/g, "\n").split("\n");
	while (lines.length > 1 && lines[lines.length - 1].trim() === "") lines.pop();
	if (lines.every((l) => l.trim() === "")) return null;

	const index = view.lineNumber - 1;
	const restOfLine = (view.lines[index] ?? "").slice(view.column - 1);
	const following = view.lines.slice(index + 1, index + 1 + lines.length + 1);
	// The guess ends a line the buffer already continues: its trailing lines are
	// compared with the buffer's next lines, longest overlap first.
	for (let k = Math.min(lines.length - 1, following.length); k > 0; k--) {
		const tail = lines.slice(lines.length - k).map((l) => l.trim());
		const next = following.slice(0, k).map((l) => l.trim());
		if (tail.every((l, i) => l === next[i])) {
			lines = lines.slice(0, lines.length - k);
			break;
		}
	}
	if (lines.every((l) => l.trim() === "")) return null;
	// A single-line guess that IS the next line, at the end of an empty line.
	if (lines.length === 1 && restOfLine.trim() === "") {
		const nextNonBlank = view.lines.slice(index + 1).find((l) => l.trim() !== "");
		const typed = (view.lines[index] ?? "").slice(0, view.column - 1);
		if (nextNonBlank !== undefined && `${typed}${lines[0]}`.trim() === nextNonBlank.trim()) return null;
	}
	return lines.join("\n");
}
