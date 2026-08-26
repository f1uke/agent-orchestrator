import type { components } from "../../../api/schema";

type Diff = components["schemas"]["DiffContextResponse"];

/**
 * One contiguous run of changed lines, in NEW-side coordinates, carrying the OLD
 * lines it replaced.
 *
 * The old text is what makes Discard Change possible without a second git call:
 * the per-line change map the file read returns (`changedLines`) says WHERE a
 * hunk is but not what it replaced, so restoring one from that alone is not
 * possible.
 */
export type Hunk = {
	/** 1-based, inclusive. A pure deletion has start === end: the line it sits above. */
	start: number;
	end: number;
	kind: "added" | "modified" | "removed";
	/** The old-side lines this run replaced, verbatim and newline-free. Empty for "added". */
	oldText: string[];
};

/**
 * Split a file's diff into contiguous change runs.
 *
 * Deliberately mirrors the daemon's own `diffhunk.ChangedLines`, run classifier
 * for run classifier, so the two gutter lanes cannot disagree about what counts
 * as one hunk: additions + deletions is `modified`, additions alone is `added`,
 * and deletions alone is a zero-height `removed` marker on the new-side line the
 * removed content used to precede (one past the last line, at end of file).
 */
export function hunksOf(diff: Diff | undefined): Hunk[] {
	if (!diff?.available) return [];
	const out: Hunk[] = [];
	let adds = 0;
	let firstAdd = 0;
	let dels: string[] = [];
	// The new-side line a deletion-only run sits above. Tracked as the diff is
	// walked, because at end of file there is no following context row to read it
	// from.
	let delBoundary = 1;
	let lastNewLine = 0;

	const flush = () => {
		if (adds > 0) {
			out.push({
				start: firstAdd,
				end: firstAdd + adds - 1,
				kind: dels.length > 0 ? "modified" : "added",
				oldText: dels,
			});
		} else if (dels.length > 0) {
			out.push({ start: delBoundary, end: delBoundary, kind: "removed", oldText: dels });
		}
		adds = 0;
		firstAdd = 0;
		dels = [];
	};

	for (const line of diff.lines) {
		switch (line.kind) {
			case "add":
				if (adds === 0) firstAdd = line.newLine;
				adds++;
				lastNewLine = line.newLine;
				break;
			case "del":
				if (adds === 0 && dels.length === 0) delBoundary = lastNewLine + 1;
				dels.push(line.text);
				break;
			default:
				// context, and the "hunk" skip marker: both end the current run.
				flush();
				if (line.newLine > 0) lastNewLine = line.newLine;
				break;
		}
	}
	flush();
	return out;
}

/**
 * The new-side lines the BRANCH gutter lane marks.
 *
 * 🗝 A flat list of line numbers, with no kind attached, and that is the design
 * rather than an omission. Colouring this lane by kind the way the uncommitted
 * lane is coloured was tried and was wrong: on a branch under review nearly
 * every line is changed, so two kind-coloured bars sit side by side and read as
 * ONE thick bar. This lane's only question is "is this line part of what my
 * branch changed"; which kind it is already lives in the rail's `+N −M`.
 */
export function branchLaneLines(diff: Diff | undefined): number[] {
	const out: number[] = [];
	for (const hunk of hunksOf(diff)) {
		for (let line = hunk.start; line <= hunk.end; line++) out.push(line);
	}
	return out;
}

/** The line a Changes row should open on. Null when the file has no hunks. */
export function firstHunkLine(diff: Diff | undefined): number | null {
	return hunksOf(diff)[0]?.start ?? null;
}

/**
 * The file as it was at the diff's base, replayed from the payload's old side.
 *
 * ⛔ Returns null unless the payload carries EVERY line. `git diff` defaults to
 * three lines of context, so an ordinary payload is hunks with the rest of the
 * file missing — replaying that would produce an "original" that is confidently
 * wrong and a diff editor full of invented changes. Two things prove the payload
 * is partial and both are refused: the server's own `truncated` flag, and a
 * `hunk` skip marker, which the daemon emits exactly where it left lines out.
 * Ask for it with `fullContext: true`.
 */
export function originalTextFrom(diff: Diff | undefined): string | null {
	if (!diff?.available || diff.truncated) return null;
	const rows: string[] = [];
	for (const line of diff.lines) {
		if (line.kind === "hunk") return null;
		if (line.oldLine > 0) rows.push(line.text);
	}
	return rows.join("\n");
}
