import type { NextEditRequest } from "../../../main/inline-completion/next-edit";
import type { EditHistory } from "./edit-history";
import type { WindowView } from "./next-edit-suggestion";

/**
 * The window a next-edit model rewrites: a FIXED 10 lines above the cursor's
 * line and 10 below, clamped to the file. Sweep trained on exactly this
 * (blog.sweep.dev/posts/oss-next-edit, "Sweep's Editable Window") and found a
 * fixed size beats syntax-aware boundaries, so it is not a tuning knob.
 */
export const WINDOW_LINES_ABOVE = 10;
export const WINDOW_LINES_BELOW = 10;
/** How many of the file's lines after the window a rewrite is checked against (see `dropSpilledLines`). */
const SPILL_LINES = 8;

/**
 * The request for the cursor at (1-based) `lineNumber`/`column`, with the view
 * needed to read the answer back - or null when the file is not followed.
 */
export function buildNextEditRequest(
	history: EditHistory,
	key: string,
	path: string,
	lineNumber: number,
	column: number,
): { request: NextEditRequest; view: WindowView } | null {
	const lines = history.lines(key);
	if (!lines) return null;
	const cursor = Math.min(lineNumber - 1, lines.length - 1);
	const start = Math.max(0, cursor - WINDOW_LINES_ABOVE);
	const end = Math.min(lines.length - 1, cursor + WINDOW_LINES_BELOW);
	const current = lines.slice(start, end + 1).join("\n");
	const original = (history.windowBeforeLatest(key, start, end) ?? lines.slice(start, end + 1)).join("\n");
	return {
		request: { path, recent: history.recent(), original, current },
		view: {
			current,
			original,
			firstLineNumber: start + 1,
			lineNumber,
			column,
			after: lines.slice(end + 1, end + 1 + SPILL_LINES),
		},
	};
}

/** A cache key for a request: the same question gets the same answer (greedy decoding). */
export function nextEditKey(request: NextEditRequest): string {
	return JSON.stringify(request);
}
