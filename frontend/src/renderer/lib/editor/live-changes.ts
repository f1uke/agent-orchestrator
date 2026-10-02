import type { components } from "../../../api/schema";
import { diffLines, type LineRun } from "./line-diff";

type LineChange = components["schemas"]["LineChangeDTO"];

/**
 * One contiguous run of changed lines, in the BUFFER's coordinates, carrying the
 * base lines it replaced - which is what lets Discard Change put a hunk back
 * without asking git again.
 */
export type Hunk = {
	/** 1-based, inclusive. A pure deletion has start === end: the line it sits above. */
	start: number;
	end: number;
	kind: "added" | "modified" | "removed";
	/** The base-side lines this run replaced, verbatim and newline-free. Empty for "added". */
	oldText: string[];
};

/**
 * A text as lines the way the editor's model holds them: no terminators, and no
 * phantom empty line after a final newline (the model holds a file WITHOUT its
 * final newline; `fileBytes` puts it back at save).
 *
 * 🗝 An empty text and a lone empty line are the SAME zero lines, on every side.
 * A model is never shorter than one line, so an empty file's model is `[""]`;
 * reading that as one line while an empty base reads as none would mark an
 * untouched empty file as changed.
 */
export function linesOf(text: string): string[] {
	const body = text.endsWith("\r\n") ? text.slice(0, -2) : text.endsWith("\n") ? text.slice(0, -1) : text;
	return body === "" ? [] : body.split(/\r\n|\n/);
}

/** The model's own lines, under the same empty-file rule as `linesOf`. */
export function modelLines(lines: string[]): string[] {
	return lines.length === 1 && lines[0] === "" ? [] : lines;
}

/**
 * The runs that turn `base` into `current`, as hunks in CURRENT-side
 * coordinates - the same shape the per-file diff's hunks have, so the gutter and
 * Discard Change take either.
 *
 * Classified the way git's own hunks are (`diffhunk.ChangedLines`): lines replaced is
 * `modified`, lines only inserted is `added`, and lines only removed is a
 * zero-height `removed` marker on the line the removed text used to precede (one
 * past the last line, at end of file).
 */
export function hunksBetween(base: readonly string[], current: readonly string[]): Hunk[] {
	return diffLines(base, current).map((run) => hunkOf(run, base));
}

function hunkOf(run: LineRun, base: readonly string[]): Hunk {
	const oldText = base.slice(run.a0, run.a1);
	if (run.b1 > run.b0) {
		return { start: run.b0 + 1, end: run.b1, kind: oldText.length > 0 ? "modified" : "added", oldText };
	}
	return { start: run.b0 + 1, end: run.b0 + 1, kind: "removed", oldText };
}

/** The editor's three change lanes, every one measured against the LIVE buffer. */
export type LiveLanes = {
	/** merge-base(target, branch) vs buffer: everything this branch changed. */
	branch: Hunk[];
	/** HEAD vs buffer: what is not committed yet, saved or not. */
	uncommitted: Hunk[];
	/** The text the buffer was loaded or last saved from vs buffer: not saved yet. */
	unsaved: Hunk[];
	/**
	 * The uncommitted hunks Discard Change can restore. Empty when HEAD's text is
	 * unknown: a hunk carried over from the disk-based map knows where it is but
	 * not what it replaced.
	 */
	discardable: Hunk[];
};

export type LiveLaneInput = {
	current: readonly string[];
	saved: readonly string[];
	/** The file at HEAD; null when unknown (no base could be read). */
	head: readonly string[] | null;
	/** The file at merge-base(target, branch); null when unknown. */
	target: readonly string[] | null;
	/**
	 * The daemon's working-tree-vs-HEAD map of the file ON DISK. Used only when
	 * `head` is unknown, carried through the unsaved edits so its marks stay on
	 * the lines they describe instead of on whatever moved under them.
	 */
	diskUncommitted: readonly LineChange[];
};

/**
 * Measure the buffer against all three bases.
 *
 * 🗝 Every lane is measured against the BUFFER, never against the file on disk.
 * A gutter computed from disk is right until the first keystroke and wrong from
 * then until ⌘S - the line just typed has no mark, and every mark below it sits
 * one line off. That is the bug this replaces.
 */
export function liveLanes(input: LiveLaneInput): LiveLanes {
	const unsavedRuns = diffLines(input.saved, input.current);
	const unsaved = unsavedRuns.map((run) => hunkOf(run, input.saved));
	const branch = input.target ? hunksBetween(input.target, input.current) : [];
	if (input.head) {
		const uncommitted = hunksBetween(input.head, input.current);
		return { branch, uncommitted, unsaved, discardable: uncommitted };
	}
	return {
		branch,
		uncommitted: carryThrough(input.diskUncommitted, unsavedRuns, input.current.length),
		unsaved,
		discardable: [],
	};
}

/**
 * Re-home a disk-based change map onto the buffer.
 *
 * A saved line that is still in the buffer keeps its mark, shifted by whatever
 * was inserted or removed above it. A saved line the buffer has since edited
 * drops it: whether that line still differs from HEAD is unknowable without
 * HEAD's text, and the unsaved lane already marks it.
 */
function carryThrough(changes: readonly LineChange[], runs: readonly LineRun[], lineCount: number): Hunk[] {
	const mapLine = (line: number): number | null => {
		const index = line - 1;
		let shift = 0;
		for (const run of runs) {
			if (run.a0 > index) break;
			if (index < run.a1) return null;
			shift += run.b1 - run.b0 - (run.a1 - run.a0);
		}
		return line + shift;
	};
	const out: Hunk[] = [];
	for (const change of changes) {
		const kind = change.kind as Hunk["kind"];
		if (kind !== "added" && kind !== "modified" && kind !== "removed") continue;
		if (kind === "removed") {
			const at = mapLine(change.start);
			if (at !== null)
				out.push({ start: Math.min(at, lineCount + 1), end: Math.min(at, lineCount + 1), kind, oldText: [] });
			continue;
		}
		// Line by line, then re-joined: an edit in the middle of a run splits it.
		let open: Hunk | null = null;
		for (let line = change.start; line <= Math.max(change.end, change.start); line++) {
			const at = mapLine(line);
			if (at === null || at > lineCount) {
				open = null;
				continue;
			}
			if (open && open.end === at - 1) {
				open.end = at;
				continue;
			}
			open = { start: at, end: at, kind, oldText: [] };
			out.push(open);
		}
	}
	return out;
}

/** How many buffer lines a set of hunks marks; a removal counts as one, as the header always has. */
export function markedLineCount(hunks: readonly Hunk[]): number {
	let n = 0;
	for (const hunk of hunks) n += hunk.kind === "removed" ? 1 : hunk.end - hunk.start + 1;
	return n;
}
