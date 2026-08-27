import type { components } from "../../../api/schema";

export type WorkspaceSearchResponse = components["schemas"]["WorkspaceSearchResponse"];
export type WorkspaceSearchFile = components["schemas"]["WorkspaceSearchFileDTO"];
export type WorkspaceSearchMatch = components["schemas"]["WorkspaceSearchMatchDTO"];

/**
 * One row in the ⌘⇧F results list — a file header, or one match under it.
 *
 * A FLAT list of two row kinds rather than a nested structure, because the list
 * is virtualised: the windowing library asks "what is at index N" and a tree
 * cannot answer that without being flattened first anyway. Both kinds render at
 * the SAME fixed height, which is what lets the virtualiser's size estimate be
 * exact — #254 measured content-sized rows at 29.75px and 30.5px, and half a
 * pixel per row compounds into thousands across a big result set.
 */
export type SearchRow =
	| { kind: "file"; key: string; file: WorkspaceSearchFile }
	| { kind: "match"; key: string; path: string; match: WorkspaceSearchMatch };

/**
 * The results, flattened for the virtualiser.
 *
 * `collapsed` names the files whose matches are folded away; a folded file keeps
 * its header row, which is how the reader gets it back.
 */
export function searchRows(
	files: readonly WorkspaceSearchFile[],
	collapsed: ReadonlySet<string>,
): readonly SearchRow[] {
	const rows: SearchRow[] = [];
	for (const file of files) {
		rows.push({ kind: "file", key: `f:${file.path}`, file });
		if (collapsed.has(file.path)) continue;
		for (const match of file.matches) {
			// Line AND column, because a line can hold the same term twice and two
			// rows with the same key would collapse into one under React.
			rows.push({ kind: "match", key: `m:${file.path}:${match.line}:${match.column}`, path: file.path, match });
		}
	}
	return rows;
}

/** How a file header reports its matches: "12", or "100 of 512" when capped. */
export function fileCountLabel(file: WorkspaceSearchFile): string {
	if (!file.truncated || file.matches.length >= file.total) return String(file.total);
	return `${file.matches.length} of ${file.total}`;
}

/**
 * The one-line summary above the list.
 *
 * It reports the HONEST totals — the whole tree is scanned even though only a
 * prefix travels back — and says out loud when the list is a prefix. Silent
 * truncation reads as "that's all there is", which is the trap #254 named.
 */
export function searchSummary(res: WorkspaceSearchResponse): string {
	// Nothing found is reported HERE and nowhere else — no centred empty state.
	// Every prefix of a word that has not matched yet is a no-results answer, so
	// this is a state the reader passes through constantly while typing; a
	// full-height illustration appearing and vanishing between keystrokes is the
	// wrong weight for it. Saying how much was searched is what makes the line
	// worth reading: "nothing" and "nothing, in 4,488 files" are different facts.
	if (res.totalMatches === 0) {
		return `No results in ${res.filesSearched.toLocaleString()} ${res.filesSearched === 1 ? "file" : "files"}`;
	}
	const matches = `${res.totalMatches.toLocaleString()} ${res.totalMatches === 1 ? "result" : "results"}`;
	const files = `${res.totalFiles.toLocaleString()} ${res.totalFiles === 1 ? "file" : "files"}`;
	const shown = res.files.reduce((n, f) => n + f.matches.length, 0);
	const head = `${matches} in ${files}`;
	return res.truncated && shown < res.totalMatches ? `${head} — showing ${shown.toLocaleString()}` : head;
}

/**
 * A match preview split into the three spans a row draws: what precedes the
 * match, the match itself, and what follows.
 *
 * JavaScript string indices are UTF-16 code units and the server's offsets are
 * UTF-16 too, so these slices need no conversion — which is the whole reason the
 * server sends UTF-16 rather than bytes. Offsets are clamped because a row must
 * paint even if a future server sends something inconsistent.
 */
export function splitPreview(match: WorkspaceSearchMatch): { before: string; hit: string; after: string } {
	const text = match.preview;
	const start = Math.max(0, Math.min(match.previewStart, text.length));
	const end = Math.max(start, Math.min(match.previewEnd, text.length));
	return { before: text.slice(0, start), hit: text.slice(start, end), after: text.slice(end) };
}

/**
 * Leading whitespace trimmed off a preview, with the highlight moved to match.
 *
 * Source lines are indented and the rail is ~330px wide; drawing six levels of
 * indentation before the first character spends the row on nothing. The match
 * offsets have to move by exactly what was removed or the highlight lands on the
 * wrong characters — which is why this is one function rather than a `trimStart`
 * at the call site.
 */
export function trimPreviewIndent(match: WorkspaceSearchMatch): WorkspaceSearchMatch {
	const trimmed = match.preview.replace(/^[ \t]+/, "");
	const removed = match.preview.length - trimmed.length;
	if (removed === 0) return match;
	return {
		...match,
		preview: trimmed,
		previewStart: Math.max(0, match.previewStart - removed),
		previewEnd: Math.max(0, match.previewEnd - removed),
	};
}
