import { useState } from "react";
import type { WikiTaskRow } from "../../renderer/hooks/useWiki";
import { fromTagAddsSomething, sourceLabel, splitFromTags, splitWikilinks } from "./task-text";

/**
 * Where the `(from: …)` tag goes. The one thing this preview exists to let the
 * reader choose between, so the three treatments live side by side rather than
 * one of them being written into the row and the others described.
 */
export type FromTreatment =
	/** Drop it when the row already says it; keep it as a quiet chip when it does not. */
	| "smart"
	/** Never in the sentence; always a quiet chip on the meta line. */
	| "always"
	/** Never at rest; it appears on the meta line when the row is hovered. */
	| "hover";

export function TaskRowNext({
	row,
	treatment,
	onOpenWikilink,
	onOpenSource,
}: {
	row: WikiTaskRow;
	treatment: FromTreatment;
	onOpenWikilink: (target: string, anchor: string) => void;
	onOpenSource: (path: string, line: number) => void;
}) {
	const [hovered, setHovered] = useState(false);
	const [ticked, setTicked] = useState(false);

	// The sentence, with provenance lifted out of it in every treatment. The
	// task is the row; nothing else may sit in the same weight as the task.
	const { text, tags } = splitFromTags(row.text);
	const section = row.section ?? "";
	const subsection = row.subsection ?? "";
	const kept = treatment === "smart" ? tags.filter((tag) => fromTagAddsSomething(tag, section, subsection)) : tags;
	const showChips = kept.length > 0 && (treatment !== "hover" || hovered);

	const where = sourceLabel(row.path) + (section ? ` · ${section}` : "");

	return (
		<div
			className={`wiki-tasks__row wiki-tasks__row--next${ticked ? " is-done" : ""}`}
			onMouseEnter={() => setHovered(true)}
			onMouseLeave={() => setHovered(false)}
		>
			<button
				type="button"
				className="wiki-tasks__box wiki-tasks__box--next"
				aria-label={`Tick off: ${text}`}
				aria-pressed={ticked}
				onClick={() => setTicked((on) => !on)}
			>
				{ticked ? <CheckMark /> : null}
			</button>
			<div className="wiki-tasks__body">
				{/*
				 * Still not markup: `splitWikilinks` hands back tokens and each
				 * one becomes an element here, so a row full of angle brackets
				 * shows angle brackets. Only `[[…]]` becomes a link, and it
				 * borrows the Notes tab's own class rather than inventing a
				 * second look for the same thing.
				 */}
				<span className="wiki-tasks__text wiki-tasks__text--next">
					{splitWikilinks(text).map((part, index) =>
						part.kind === "text" ? (
							<span key={index}>{part.value}</span>
						) : (
							<button
								key={index}
								type="button"
								className="note-prose__wikilink note-prose__wikilink--active"
								title={part.anchor ? `${part.target} › ${part.anchor}` : part.target}
								onClick={() => onOpenWikilink(part.target, part.anchor)}
							>
								{part.label}
							</button>
						),
					)}
				</span>
				<span className="wiki-tasks__meta wiki-tasks__meta--next">
					{row.owner && <span className="wiki-tasks__owner">@{row.owner}</span>}
					{/*
					 * The address, and the quietest thing in the row. It goes to
					 * the row's own LINE, not merely the file — the reader who
					 * clicks it is asking "what does this sit next to", and a
					 * note scrolled to the top does not answer that.
					 */}
					<button
						type="button"
						className="wiki-tasks__where wiki-tasks__where--next"
						title={`${row.path}:${row.line}`}
						onClick={() => onOpenSource(row.path, row.line)}
					>
						{where}
					</button>
					{showChips &&
						kept.map((tag) => (
							<span key={tag} className="wiki-tasks__from" title={`from: ${tag}`}>
								<span className="wiki-tasks__from-label">from</span> {tag}
							</span>
						))}
				</span>
			</div>
		</div>
	);
}

/** The shipped row, unchanged, so the comparison is a comparison. */
export function TaskRowToday({
	row,
	onOpenSource,
}: {
	row: WikiTaskRow;
	onOpenSource: (path: string, line: number) => void;
}) {
	const [ticked, setTicked] = useState(false);
	return (
		<div className={`wiki-tasks__row${ticked ? " is-done" : ""}`}>
			<button
				type="button"
				className="wiki-tasks__box"
				aria-label={`Tick off: ${row.text}`}
				onClick={() => setTicked((on) => !on)}
			>
				{ticked ? <CheckMark /> : null}
			</button>
			<div className="wiki-tasks__body">
				<span className="wiki-tasks__text">{row.text}</span>
				<span className="wiki-tasks__meta">
					{row.owner && <span className="wiki-tasks__owner">@{row.owner}</span>}
					<button
						type="button"
						className="wiki-tasks__where"
						title={row.path}
						onClick={() => onOpenSource(row.path, row.line)}
					>
						{noteLabelToday(row.path)}
						{row.section ? ` · ${row.section}` : ""}
					</button>
				</span>
			</div>
		</div>
	);
}

/** `noteLabel` as it ships today (`components/WikiTasksPanel.tsx`). */
function noteLabelToday(path: string): string {
	const segments = path.split("/");
	const base = (segments.pop() ?? path).replace(/\.md$/i, "");
	const folder = segments.pop();
	return folder ? `${folder}/${base}` : base;
}

function CheckMark() {
	return (
		<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" aria-hidden="true">
			<path d="M20 6 9 17l-5-5" strokeLinecap="round" strokeLinejoin="round" />
		</svg>
	);
}
