import type { WorkspaceFileOpen } from "../open-workspace-file";

/**
 * Back/forward for the file viewer — the navigation history Monaco standalone
 * does not have.
 *
 * A pure model over the ONE seam every jump already goes through
 * (`openWorkspaceFile` in `SessionView`), which is what makes it cover ⌘click /
 * go-to-definition, ⌘⇧O, peek-then-open, a Changes row and a Browse row without
 * any of them knowing history exists. Back that works after some jumps and not
 * others is worse than no back at all, so the seam is the only place this is
 * wired in.
 *
 * Browser semantics, because they are the ones everybody already has: a jump
 * from anywhere but the end truncates forward, and going back and forward
 * changes only where you are.
 */

/** Where a jump landed. `WorkspaceFileOpen` minus the fields about HOW it was opened. */
export type HistoryEntry = {
	path: string;
	line?: number;
	column?: number;
	inWorkspace?: boolean;
	focus?: "first-hunk";
};

export type FileHistory = {
	entries: readonly HistoryEntry[];
	/** Index of the current entry; -1 when the stack is empty. */
	index: number;
};

export const emptyHistory: FileHistory = { entries: [], index: -1 };

/**
 * How many jumps back you can go.
 *
 * Deep enough that a real afternoon of chasing definitions is covered, bounded
 * because this lives for as long as the app runs.
 */
const MAX_ENTRIES = 50;

export function currentEntry(history: FileHistory): HistoryEntry | null {
	return history.entries[history.index] ?? null;
}

export function canGoBack(history: FileHistory): boolean {
	return history.index > 0;
}

export function canGoForward(history: FileHistory): boolean {
	return history.index >= 0 && history.index < history.entries.length - 1;
}

export function backTarget(history: FileHistory): HistoryEntry | null {
	return canGoBack(history) ? history.entries[history.index - 1] : null;
}

export function forwardTarget(history: FileHistory): HistoryEntry | null {
	return canGoForward(history) ? history.entries[history.index + 1] : null;
}

function entryOf(file: WorkspaceFileOpen): HistoryEntry {
	return { path: file.path, line: file.line, column: file.column, inWorkspace: file.inWorkspace, focus: file.focus };
}

/**
 * Is opening `file` a move at all, or the reader landing where they already are?
 *
 * A jump WITHIN a file counts when it names a different line — that is exactly
 * what makes ⌘click on a symbol defined thirty lines up reversible. Re-opening
 * the same place (clicking the same rail row twice, ⌘⇧O onto the open file) does
 * not, because a stack full of duplicates makes Back do nothing visible, which
 * reads as broken.
 */
function isSamePlace(entry: HistoryEntry | null, file: WorkspaceFileOpen): boolean {
	if (!entry || entry.path !== file.path) return false;
	// An open with no line means "this file, wherever it opens" — already true.
	if (file.line == null) return true;
	return entry.line === file.line;
}

/**
 * Records a jump, truncating anything ahead of the current position.
 *
 * `from` is where the reader actually WAS when they jumped — Monaco's live
 * cursor, not the line the current entry was opened at. Without it, ⌘click at
 * line 400 of a long file and then Back lands at line 1, which is somewhere but
 * not where they expected; with it, Back lands at 400. It is ignored when it
 * names a different file than the current entry, because then it is stale.
 */
export function pushHistory(
	history: FileHistory,
	file: WorkspaceFileOpen,
	from?: { path: string; line: number; column?: number },
): FileHistory {
	const marked = markPosition(history, from);
	if (isSamePlace(currentEntry(marked), file)) return marked;
	const kept = marked.entries.slice(0, marked.index + 1);
	const entries = [...kept, entryOf(file)].slice(-MAX_ENTRIES);
	return { entries, index: entries.length - 1 };
}

/** Updates the current entry with where the cursor actually sits. */
export function markPosition(
	history: FileHistory,
	from?: { path: string; line: number; column?: number },
): FileHistory {
	const current = currentEntry(history);
	if (!from || !current || current.path !== from.path) return history;
	if (current.line === from.line && current.column === from.column) return history;
	const entries = history.entries.slice();
	entries[history.index] = { ...current, line: from.line, column: from.column };
	return { entries, index: history.index };
}

/** Moves back one entry and returns the new state; unchanged when there is nowhere to go. */
export function goBack(history: FileHistory, from?: { path: string; line: number; column?: number }): FileHistory {
	if (!canGoBack(history)) return history;
	const marked = markPosition(history, from);
	return { entries: marked.entries, index: marked.index - 1 };
}

export function goForward(history: FileHistory, from?: { path: string; line: number; column?: number }): FileHistory {
	if (!canGoForward(history)) return history;
	const marked = markPosition(history, from);
	return { entries: marked.entries, index: marked.index + 1 };
}

/**
 * The file to open for an entry.
 *
 * `focus: "first-hunk"` is dropped once an entry carries a line: it means "land
 * on what this branch changed" for a Changes row's FIRST open, and returning to
 * an entry means returning to the line you left, not re-running that rule.
 */
export function entryToOpen(entry: HistoryEntry): WorkspaceFileOpen {
	return {
		path: entry.path,
		line: entry.line,
		column: entry.column,
		inWorkspace: entry.inWorkspace,
		focus: entry.line == null ? entry.focus : undefined,
		reveal: "follow",
	};
}
