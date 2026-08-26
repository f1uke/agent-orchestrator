import type { components } from "../../../api/schema";

type LineChange = components["schemas"]["LineChangeDTO"];

/** One gutter mark, in Monaco's 1-based line coordinates. */
export type GutterMark = { line: number; className: string };

/** The uncommitted lane's classes; colours live in `styles.css` beside the diff tokens. */
const UNCOMMITTED_CLASS: Record<string, string> = {
	added: "ao-change-bar ao-change-bar--added",
	modified: "ao-change-bar ao-change-bar--modified",
	removed: "ao-change-bar ao-change-bar--removed",
};

/** The branch lane's single class. There is deliberately only one — see below. */
export const BRANCH_LANE_CLASS = "ao-branch-bar";

function clamp(line: number, lineCount: number): number {
	return Math.min(Math.max(line, 1), Math.max(lineCount, 1));
}

/**
 * The UNCOMMITTED lane: working tree vs HEAD, coloured by kind, in the same
 * three colours the diff rows use. This is the lane whose bars can be clicked to
 * discard.
 */
export function uncommittedMarks(changes: readonly LineChange[], lineCount: number): GutterMark[] {
	const out: GutterMark[] = [];
	for (const change of changes) {
		const className = UNCOMMITTED_CLASS[change.kind];
		if (!className) continue;
		if (change.kind === "removed") {
			out.push({ line: clamp(change.start, lineCount), className });
			continue;
		}
		const start = clamp(change.start, lineCount);
		const end = clamp(Math.max(change.end, change.start), lineCount);
		for (let line = start; line <= end; line++) out.push({ line, className });
	}
	return out;
}

/**
 * The BRANCH lane: merge-base(target, HEAD) vs working tree — everything this
 * branch did, committed or not.
 *
 * 🗝 One neutral class for every line, never coloured by kind. That was tried the
 * other way in the spike and was wrong: on a branch under review nearly every
 * line is changed, so a kind-coloured branch bar sat beside the kind-coloured
 * uncommitted bar and the pair read as ONE thick bar. This lane answers only
 * "is this line part of what my branch changed"; which kind it is already lives
 * in the rail's `+N −M`.
 */
export function branchMarks(lines: readonly number[], lineCount: number): GutterMark[] {
	const seen = new Set<number>();
	const out: GutterMark[] = [];
	for (const line of lines) {
		const at = clamp(line, lineCount);
		if (seen.has(at)) continue;
		seen.add(at);
		out.push({ line: at, className: BRANCH_LANE_CLASS });
	}
	return out;
}
