import type { Hunk } from "./live-changes";

/**
 * On both GIT lanes. The gutter margin also holds Monaco's folding controls, so a
 * click has to be attributable to one of ours before it opens the discard
 * popover - and the two lanes share one node per line in that margin, so either
 * one can be the element under the cursor.
 */
export const GUTTER_LANE_CLASS = "ao-gutter-lane";

export type LaneName = "branch" | "uncommitted" | "unsaved";

/** One lane's mark over a run of lines, in Monaco's 1-based inclusive lines. */
export type LaneMark = {
	lane: LaneName;
	kind: Hunk["kind"];
	start: number;
	end: number;
	/** For the two git lanes: the glyph-margin node's classes. */
	glyphClassName?: string;
	/** For the unsaved lane: the line-number cell's classes. */
	lineNumberClassName?: string;
};

/*
 * 🗝 Every class is LANE-SCOPED, kind included (`ao-branch-bar--added`, never a
 * shared `--added`). The glyph margin holds ONE node per line, so on a line in
 * both git lanes Monaco concatenates both lanes' classes onto that node - and a
 * shared kind class would then colour whichever lane's bar it reached first,
 * with the other lane's kind on it.
 */
const GLYPH_PREFIX: Record<Exclude<LaneName, "unsaved">, string> = {
	branch: "ao-branch-bar",
	uncommitted: "ao-change-bar",
};

/**
 * The three lanes as gutter marks.
 *
 * - The two GIT lanes - branch (merge-base of the target) and uncommitted
 *   (HEAD) - draw in the glyph margin, coloured by kind.
 * - The UNSAVED lane draws on the line NUMBER: a different place and a
 *   different shape, so a line that is both unsaved and changed against git
 *   reads as both at once rather than as one ambiguous bar.
 *
 * A removal past the last line has no line to sit above, so it moves to the
 * last line's bottom edge (`--end`) instead of pretending to be above it.
 */
export function laneMarks(
	lanes: { branch: readonly Hunk[]; uncommitted: readonly Hunk[]; unsaved: readonly Hunk[] },
	lineCount: number,
): LaneMark[] {
	const last = Math.max(lineCount, 1);
	const out: LaneMark[] = [];
	for (const lane of ["branch", "uncommitted", "unsaved"] as const) {
		for (const hunk of lanes[lane]) {
			const atEnd = hunk.kind === "removed" && hunk.start > last;
			const start = Math.min(Math.max(hunk.start, 1), last);
			const end = hunk.kind === "removed" ? start : Math.min(Math.max(hunk.end, start), last);
			const mark: LaneMark = { lane, kind: hunk.kind, start, end };
			if (lane === "unsaved") {
				mark.lineNumberClassName =
					hunk.kind === "removed"
						? `ao-unsaved-removed${atEnd ? " ao-unsaved-removed--end" : ""}`
						: "ao-unsaved-line";
			} else {
				const prefix = GLYPH_PREFIX[lane];
				mark.glyphClassName = `${GUTTER_LANE_CLASS} ${prefix} ${prefix}--${hunk.kind}${atEnd ? ` ${prefix}--end` : ""}`;
			}
			out.push(mark);
		}
	}
	return out;
}
