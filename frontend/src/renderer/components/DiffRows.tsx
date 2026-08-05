import type { ReactNode } from "react";
import type { components } from "../../api/schema";
import { ACCENT, DIFF_ROW, MONO, PALETTE as P, tokenizeCode } from "../lib/comment-inbox";

type DiffLine = components["schemas"]["DiffContextLineDTO"];

/** An uncommitted-change gutter marker kind (Xcode-style bar). */
export type ChangeMark = "added" | "modified" | "removed";

// Gutter bar colour per change kind, from app design tokens (not raw Xcode
// blue): added = success green, modified = accent blue, removed = error red.
const CHANGE_BAR_COLOR: Record<ChangeMark, string> = {
	added: DIFF_ROW.addSign,
	modified: ACCENT,
	removed: P.red,
};

// Two densities: the inline rail diff is cramped ("narrow"); the full-file
// viewer in the center pane has room to breathe ("wide"). Verbatim metrics from
// the design's narrow/wide `styleDiffRow` variants.
const METRICS = {
	narrow: { num: 34, numPad: 8, sign: 14, fontSize: 11, lineHeight: 1.7, textPad: 10 },
	wide: { num: 44, numPad: 12, sign: 16, fontSize: 12, lineHeight: 1.85, textPad: 14 },
} as const;

type Metrics = (typeof METRICS)[keyof typeof METRICS];

/** The `kind` the backend uses for a seam where the diff skips lines. */
const HUNK = "hunk";

/**
 * How many lines the diff skips before the hunk marker at `index`, derived from
 * the highest line number rendered above it. Counted on the new side, falling
 * back to the old side for a region that exists only there (a deletion). 0 means
 * "unknown", which only happens for malformed input — the separator then shows
 * its `@@` range alone rather than a wrong count.
 */
function hiddenLineCount(lines: DiffLine[], index: number): number {
	let oldEnd = 0;
	let newEnd = 0;
	for (let i = 0; i < index; i++) {
		const l = lines[i];
		if (l.kind === HUNK) continue;
		oldEnd = Math.max(oldEnd, l.oldLine);
		newEnd = Math.max(newEnd, l.newLine);
	}
	const mark = lines[index];
	const [start, end] = mark.newLine > 0 ? [mark.newLine, newEnd] : [mark.oldLine, oldEnd];
	return Math.max(start - end - 1, 0);
}

/**
 * A separator standing in for the lines a diff skips, so two distant regions of
 * a file can never be read as consecutive code. Deliberately chrome rather than
 * content: a banded row with hairlines, `⋯` where a line number would be, and no
 * +/- sign — nothing that could be mistaken for an added or removed line.
 *
 * Shows git's own `@@ -a,b +c,d @@` range plus the section heading it appends
 * (the enclosing function), and how many lines are hidden.
 */
function HunkSeparator({
	line,
	hidden,
	metrics,
	indented,
}: {
	line: DiffLine;
	hidden: number;
	metrics: Metrics;
	indented: boolean;
}) {
	// git writes "@@ -a,b +c,d @@ <section heading>"; split so the range reads as
	// the primary label and the heading as its quieter companion.
	const at = line.text.indexOf(" @@");
	const range = at >= 0 ? line.text.slice(0, at + 3) : line.text;
	const heading = at >= 0 ? line.text.slice(at + 3).trim() : "";
	return (
		<div
			data-testid="hunk-separator"
			style={{
				display: "flex",
				alignItems: "center",
				background: P.pillBg,
				borderTop: `1px solid ${P.connector}`,
				borderBottom: `1px solid ${P.connector}`,
				padding: "2px 0",
			}}
		>
			{indented ? <span aria-hidden="true" style={{ flex: "none", width: 3 }} /> : null}
			<span
				aria-hidden="true"
				style={{
					flex: "none",
					width: metrics.num + metrics.sign,
					paddingRight: metrics.numPad,
					textAlign: "center",
					color: P.faint,
					userSelect: "none",
				}}
			>
				⋯
			</span>
			<span style={{ flex: "none", color: P.secondary }}>{range}</span>
			{heading !== "" ? (
				<span
					style={{
						flex: "0 1 auto",
						minWidth: 0,
						paddingLeft: 10,
						color: P.secondary2,
						overflow: "hidden",
						textOverflow: "ellipsis",
						whiteSpace: "nowrap",
					}}
				>
					{heading}
				</span>
			) : null}
			{hidden > 0 ? (
				<span
					style={{
						flex: "none",
						marginLeft: "auto",
						paddingLeft: 10,
						paddingRight: metrics.textPad,
						color: P.secondary2,
					}}
				>
					{hidden === 1 ? "1 line hidden" : `${hidden} lines hidden`}
				</span>
			) : null}
		</div>
	);
}

/**
 * A syntax-highlighted diff block: added/removed lines carry a background tint
 * and a colored sign glyph, while the code text is tokenized (see
 * `tokenizeCode`). Where the diff skips lines the backend emits a `hunk` line,
 * rendered as a `HunkSeparator` instead of code. Optionally anchors a node (e.g.
 * an inline comment card) right after a given line — used by the full-file
 * viewer to pin the review comment to its line.
 */
export function DiffRows({
	lines,
	size,
	anchorIndex,
	anchorNode,
	changeMarks,
}: {
	lines: DiffLine[];
	size: "narrow" | "wide";
	anchorIndex?: number;
	anchorNode?: ReactNode;
	/**
	 * Per-line uncommitted-change map, keyed by line index. When provided, a thin
	 * Xcode-style bar is rendered in the far-left gutter for marked lines (and a
	 * transparent spacer keeps unmarked lines aligned). Absent → the gutter is
	 * unchanged, so the Reviews diff path renders exactly as before.
	 */
	changeMarks?: ReadonlyMap<number, ChangeMark>;
}) {
	const m = METRICS[size];
	const showChangeGutter = changeMarks != null;
	return (
		<div
			className="mono"
			style={{
				fontFamily: MONO,
				fontSize: m.fontSize,
				lineHeight: m.lineHeight,
				padding: size === "wide" ? "6px 0" : 0,
			}}
		>
			{lines.map((line, i) => {
				if (line.kind === HUNK) {
					return (
						<div key={i}>
							<HunkSeparator line={line} hidden={hiddenLineCount(lines, i)} metrics={m} indented={showChangeGutter} />
							{anchorNode != null && anchorIndex === i ? anchorNode : null}
						</div>
					);
				}
				const add = line.kind === "add";
				const del = line.kind === "del";
				const mark = showChangeGutter ? changeMarks?.get(i) : undefined;
				return (
					<div key={i}>
						<div
							style={{
								display: "flex",
								background: add ? DIFF_ROW.addBg : del ? DIFF_ROW.delBg : "transparent",
								padding: "1px 0",
							}}
						>
							{showChangeGutter ? (
								<span
									aria-hidden="true"
									data-testid={mark ? `change-bar-${i}` : undefined}
									data-change={mark}
									style={{
										flex: "none",
										width: 3,
										alignSelf: "stretch",
										background: mark ? CHANGE_BAR_COLOR[mark] : "transparent",
									}}
								/>
							) : null}
							<span
								style={{
									flex: "none",
									width: m.num,
									textAlign: "right",
									paddingRight: m.numPad,
									color: P.muted3,
									userSelect: "none",
								}}
							>
								{line.newLine || line.oldLine || ""}
							</span>
							<span
								style={{
									flex: "none",
									width: m.sign,
									textAlign: "center",
									color: add ? DIFF_ROW.addSign : del ? DIFF_ROW.delSign : DIFF_ROW.contextSign,
								}}
							>
								{add ? "+" : del ? "-" : " "}
							</span>
							<span style={{ flex: 1, whiteSpace: "pre", paddingRight: m.textPad }}>
								{tokenizeCode(line.text).map((t, j) => (
									<span key={j} style={{ color: t.color }}>
										{t.text}
									</span>
								))}
							</span>
						</div>
						{anchorNode != null && anchorIndex === i ? anchorNode : null}
					</div>
				);
			})}
		</div>
	);
}
