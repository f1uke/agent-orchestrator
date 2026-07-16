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

/**
 * A syntax-highlighted diff block: added/removed lines carry a background tint
 * and a colored sign glyph, while the code text is tokenized (see
 * `tokenizeCode`). Optionally anchors a node (e.g. an inline comment card) right
 * after a given line — used by the full-file viewer to pin the review comment
 * to its line.
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
