import type { NextEditChange } from "../../../main/inline-completion/next-edit";

/**
 * What the person changed recently, as a next-edit model needs it: each change
 * as the region it touched BEFORE and AFTER, and - for the file being edited -
 * the text as it was before the latest change.
 *
 * Pure over line arrays, so it is tested directly; `provider.ts` feeds it
 * Monaco's content-change events.
 *
 * 🗝 Keystrokes are not changes. Typing a word is a dozen content events, and a
 * model shown a dozen one-character diffs learns nothing it can act on. Events
 * close to the change being made (within `GROUP_LINES`) are merged into it, the
 * way Zed groups its edit-prediction events, so "renamed this parameter" arrives
 * as one before/after pair.
 */

/** A change this many lines or fewer from the open one extends it instead of starting another. */
export const GROUP_LINES = 8;
/** Changes kept, across files. Sweep's reference client sends three. */
export const MAX_CHANGES = 3;
/** Unchanged lines shown around a change's before/after, so the model can place it. */
export const CONTEXT_LINES = 2;
/** A change spanning more than this (a paste, a reformat) says nothing a 21-line window can use. */
export const MAX_CHANGE_LINES = 60;

/** One content change, in the coordinates of the text BEFORE it (Monaco's, made 0-based). */
export type LineChange = {
	/** 0-based first line of the replaced range. */
	startLine: number;
	/** 0-based column (UTF-16 offset) in that line. */
	startColumn: number;
	endLine: number;
	endColumn: number;
	text: string;
};

type Change = {
	key: string;
	path: string;
	/** The region as it was when this change began. */
	before: string[];
	/** 0-based inclusive range of the region in the file's CURRENT text. Valid while it is its file's latest. */
	start: number;
	end: number;
	/** Set once a later change in the same file replaced it: the text no longer needs coordinates. */
	frozen: NextEditChange | null;
};

type FileState = { path: string; lines: string[]; latest: Change | null };

export class EditHistory {
	private readonly files = new Map<string, FileState>();
	/** Oldest first. */
	private changes: Change[] = [];

	/** Start following a file, from its current text. Re-tracking replaces the shadow. */
	track(key: string, path: string, text: string): void {
		const state = this.files.get(key);
		if (state) {
			state.path = path;
			state.lines = splitLines(text);
			// The text was replaced from outside (a reload): coordinates mean nothing now.
			if (state.latest) this.freeze(state.latest, state);
			state.latest = null;
			return;
		}
		this.files.set(key, { path, lines: splitLines(text), latest: null });
	}

	untrack(key: string): void {
		const state = this.files.get(key);
		if (!state) return;
		if (state.latest) this.freeze(state.latest, state);
		this.files.delete(key);
	}

	isTracked(key: string): boolean {
		return this.files.has(key);
	}

	/**
	 * Apply one content event's changes, in the order Monaco lists them (end of
	 * the file first, so each one's coordinates are still valid when it is applied).
	 */
	apply(key: string, changes: readonly LineChange[]): void {
		const state = this.files.get(key);
		if (!state) return;
		for (const change of changes) this.applyOne(state, key, change);
	}

	private applyOne(state: FileState, key: string, change: LineChange): void {
		const lines = state.lines;
		const a = Math.min(change.startLine, lines.length - 1);
		const b = Math.min(Math.max(change.endLine, a), lines.length - 1);
		const head = (lines[a] ?? "").slice(0, change.startColumn);
		const tail = (lines[b] ?? "").slice(change.endColumn);
		const replacement = splitLines(`${head}${change.text}${tail}`);
		const previous = this.changes[this.changes.length - 1];

		const open = state.latest;
		if (
			open &&
			open.frozen === null &&
			previous === open &&
			a <= open.end + GROUP_LINES &&
			b >= open.start - GROUP_LINES
		) {
			// Grow the region to cover this change; the lines it gains are unchanged
			// so far, so they are the same before and now.
			if (a < open.start) {
				open.before = [...lines.slice(a, open.start), ...open.before];
				open.start = a;
			}
			if (b > open.end) {
				open.before = [...open.before, ...lines.slice(open.end + 1, b + 1)];
				open.end = b;
			}
			open.end += replacement.length - (b - a + 1);
		} else {
			if (open) this.freeze(open, state);
			const next: Change = {
				key,
				path: state.path,
				before: lines.slice(a, b + 1),
				start: a,
				end: a + replacement.length - 1,
				frozen: null,
			};
			state.latest = next;
			this.changes.push(next);
			// Older changes than the ones a request sends are only kept while they
			// are still some file's latest (for that file's "before" window).
			if (this.changes.length > MAX_CHANGES + 1) this.changes = this.changes.slice(-(MAX_CHANGES + 1));
		}
		lines.splice(a, b - a + 1, ...replacement);
	}

	private freeze(change: Change, state: FileState): void {
		if (change.frozen) return;
		change.frozen = render(change, state);
	}

	/**
	 * The recent changes for a request, oldest first: at most `MAX_CHANGES`, none
	 * that came to nothing (typed and deleted again), none too large to help.
	 */
	recent(): NextEditChange[] {
		const out: NextEditChange[] = [];
		for (const change of this.changes) {
			const state = this.files.get(change.key);
			const rendered = change.frozen ?? (state && state.latest === change ? render(change, state) : null);
			if (!rendered || rendered.original === rendered.updated) continue;
			if (Math.max(change.before.length, change.end - change.start + 1) > MAX_CHANGE_LINES) continue;
			out.push(rendered);
		}
		return out.slice(-MAX_CHANGES);
	}

	/** The file's current lines, as this history last saw them. */
	lines(key: string): readonly string[] | null {
		return this.files.get(key)?.lines ?? null;
	}

	/**
	 * Lines `start..end` (0-based, inclusive) as they were before the latest
	 * change in this file - the `original/` block. The window itself when that
	 * change lies outside it, or when the file has none yet.
	 */
	windowBeforeLatest(key: string, start: number, end: number): string[] | null {
		const state = this.files.get(key);
		if (!state) return null;
		const now = state.lines.slice(start, end + 1);
		const latest = state.latest;
		if (!latest || latest.frozen || latest.end < start || latest.start > end) return now;
		if (Math.max(latest.before.length, latest.end - latest.start + 1) > MAX_CHANGE_LINES) return now;
		// The region swapped back to how it was. A region reaching past the window
		// is kept whole: cutting it would need a line mapping a rewrite does not have.
		const from = Math.min(start, latest.start);
		const to = Math.max(end, latest.end);
		return [...state.lines.slice(from, latest.start), ...latest.before, ...state.lines.slice(latest.end + 1, to + 1)];
	}

	clear(): void {
		this.files.clear();
		this.changes = [];
	}
}

function render(change: Change, state: FileState): NextEditChange {
	const lines = state.lines;
	const above = lines.slice(Math.max(0, change.start - CONTEXT_LINES), change.start);
	const below = lines.slice(change.end + 1, change.end + 1 + CONTEXT_LINES);
	return {
		path: change.path,
		original: [...above, ...change.before, ...below].join("\n"),
		updated: [...above, ...lines.slice(change.start, change.end + 1), ...below].join("\n"),
	};
}

function splitLines(text: string): string[] {
	return text.split(/\r\n|\r|\n/);
}
