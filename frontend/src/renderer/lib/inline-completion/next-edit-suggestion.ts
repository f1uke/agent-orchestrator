/**
 * From a rewritten window to the one thing worth showing. Pure over strings.
 *
 * The model returns the whole window; the suggestion is the smallest range
 * that differs (common prefix and suffix trimmed off), and how it is shown
 * follows the editors that established next-edit suggestions (VS Code's NES,
 * Cursor's Tab, Zed):
 *
 * - an INSERTION AT THE CURSOR is plain ghost text - the same grey text,
 *   Tab and Esc as the fill-in-the-middle models;
 * - anything else is an inline EDIT at its own location - Monaco's next-edit
 *   view: the change drawn where it would land and an arrow in the gutter;
 *   Tab jumps there, Tab again applies it, Esc dismisses it.
 */

export type NextEditSuggestion =
	| { kind: "insert"; text: string }
	| {
			kind: "edit";
			/** 1-based, Monaco's. */
			startLineNumber: number;
			startColumn: number;
			endLineNumber: number;
			endColumn: number;
			text: string;
	  };

export type WindowView = {
	/** The window sent as `current/`, lines joined with "\n". */
	current: string;
	/** The window sent as `original/`: the same lines before the latest change. */
	original: string;
	/** 1-based line number of the window's first line in the file. */
	firstLineNumber: number;
	/** The cursor, 1-based, in the file. */
	lineNumber: number;
	column: number;
	/** The file's lines right after the window, which the model never saw. */
	after: readonly string[];
};

const trailing = /\n+$/;
const squash = (s: string) => s.replace(/\s+/g, "");

/** Null when there is nothing worth showing. */
export function suggestionFrom(view: WindowView, rewritten: string): NextEditSuggestion | null {
	const current = view.current.replace(trailing, "");
	const next = dropSpilledLines(rewritten.replace(/\r\n/g, "\n").replace(trailing, ""), current, view.after);
	if (next === current) return null;
	// A change in whitespace only is churn, not a prediction: a person never
	// presses Tab to re-indent what they are writing.
	if (squash(next) === squash(current)) return null;
	// Putting back what the person just changed is the model second-guessing
	// them, the most common noise of next-edit models.
	if (next === view.original.replace(trailing, "") && view.original !== view.current) return null;

	let prefix = 0;
	const max = Math.min(current.length, next.length);
	while (prefix < max && current[prefix] === next[prefix]) prefix++;
	let suffix = 0;
	while (suffix < max - prefix && current[current.length - 1 - suffix] === next[next.length - 1 - suffix]) {
		suffix++;
	}
	const replaced = current.slice(prefix, current.length - suffix);
	const text = next.slice(prefix, next.length - suffix);

	const cursor = offsetOf(current, view.lineNumber - view.firstLineNumber, view.column - 1);
	if (replaced === "" && cursor !== null) {
		const atCursor = insertionAt(current, prefix, text, cursor);
		if (atCursor !== null) return { kind: "insert", text: atCursor };
	}

	const start = positionOf(current, prefix);
	const end = positionOf(current, current.length - suffix);
	return {
		kind: "edit",
		startLineNumber: view.firstLineNumber + start.line,
		startColumn: start.column + 1,
		endLineNumber: view.firstLineNumber + end.line,
		endColumn: end.column + 1,
		text,
	};
}

/**
 * A rewrite that runs past the window's last line is the model writing the
 * file's next lines from memory of their shape - lines that are already there,
 * just outside what it was shown. Those are dropped; anything else it added at
 * the end stays.
 */
function dropSpilledLines(next: string, current: string, after: readonly string[]): string {
	const lines = next.split("\n");
	const last = current.split("\n").at(-1);
	for (let m = Math.min(after.length, lines.length - 1); m > 0; m--) {
		const spill = lines.slice(-m);
		if (spill.every((l, i) => l.trim() === (after[i] ?? "").trim()) && lines[lines.length - m - 1] === last) {
			return lines.slice(0, -m).join("\n");
		}
	}
	return next;
}

/** Offset of (0-based line, 0-based column) in `text`, or null when out of range. */
function offsetOf(text: string, line: number, column: number): number | null {
	let offset = 0;
	for (let i = 0; i < line; i++) {
		const nl = text.indexOf("\n", offset);
		if (nl < 0) return null;
		offset = nl + 1;
	}
	const lineEnd = text.indexOf("\n", offset);
	const length = (lineEnd < 0 ? text.length : lineEnd) - offset;
	return column <= length ? offset + column : null;
}

function positionOf(text: string, offset: number): { line: number; column: number } {
	let line = 0;
	let lineStart = 0;
	for (let nl = text.indexOf("\n"); nl >= 0 && nl < offset; nl = text.indexOf("\n", nl + 1)) {
		line++;
		lineStart = nl + 1;
	}
	return { line, column: offset - lineStart };
}

/**
 * The text that, inserted at `to`, gives the same result as `text` inserted at
 * `from` - or null when no insertion at `to` can. Trimming a common prefix and
 * suffix leaves an insertion ambiguous inside a run of what is inserted ("ab|"
 * plus "b" could go before or after the existing b); this moves it to the cursor
 * when it names the same edit.
 */
function insertionAt(current: string, from: number, text: string, to: number): string | null {
	if (from === to) return text;
	const result = current.slice(0, from) + text + current.slice(from);
	const moved = result.slice(to, to + text.length);
	return current.slice(0, to) + moved + current.slice(to) === result ? moved : null;
}
