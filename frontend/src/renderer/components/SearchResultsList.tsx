import { observeElementRect, useVirtualizer } from "@tanstack/react-virtual";
import {
	ChevronDown,
	ChevronRight,
	File,
	FileCode2,
	FileCog,
	FileImage,
	FileJson2,
	FileText,
	FileType2,
} from "lucide-react";
import { useMemo, useRef } from "react";
import { type FileKind, fileKindFor } from "../lib/file-kind";
import {
	type WorkspaceSearchFile,
	type WorkspaceSearchMatch,
	fileCountLabel,
	searchRows,
	splitPreview,
	trimPreviewIndent,
} from "../lib/editor/search-results";
import { cn } from "../lib/utils";

/** Same coarse buckets the tree uses, so a file looks the same in both lists. */
const KIND_ICON: Record<FileKind, typeof File> = {
	code: FileCode2,
	data: FileJson2,
	doc: FileText,
	style: FileType2,
	image: FileImage,
	config: FileCog,
	other: File,
};

/**
 * Row heights, in px, pinned here and applied inline.
 *
 * What #254 established is that the height must be EXACT, not that it must be
 * uniform: a content-sized row measured 29.75px and 30.5px depending on its
 * icon, and half a pixel per row compounds into thousands of pixels of scrollbar
 * drift once there are thousands of rows. Two known heights are exact; the
 * virtualiser is told which one each index is.
 *
 * Matches are shorter than headers on purpose. A common term in a 7,000-file
 * project returns matches by the thousand and headers by the hundred, so the
 * rows the reader scans most are the ones worth the density.
 */
const FILE_ROW_HEIGHT = 26;
const MATCH_ROW_HEIGHT = 21;

/** As in FileTree: enough that a flick shows no gap, few enough to stay a window. */
const OVERSCAN = 12;

/** As in FileTree: jsdom has no layout, and a zero-height scroller must not mean an empty list. */
const FALLBACK_VIEWPORT = { width: 320, height: 900 };

/** Where a clicked match should open. */
export type SearchHit = { path: string; line: number; column: number };

/**
 * The ⌘⇧F results: every matching line, grouped under its file.
 *
 * VIRTUALISED, for the reason the Files tree is. "self" in the human's real iOS
 * project matches 12,847 times; even the capped 2,000 rows this panel can
 * receive would be ~20,000 DOM nodes rendered eagerly, which is the shape #254
 * measured at 568ms to paint and 292ms per keystroke. It renders the rows in
 * view and a little either side.
 *
 * It owns its own scroller for the same reason FileTree does — a virtualiser
 * needs the scrolling element — and takes the same fallback-rect treatment so
 * the rows exist under jsdom.
 */
export function SearchResultsList({
	files,
	collapsed,
	onToggleFile,
	onOpen,
	selected,
	label,
}: {
	files: readonly WorkspaceSearchFile[];
	/** Paths whose matches are folded away. The header row stays, so they come back. */
	collapsed: ReadonlySet<string>;
	onToggleFile: (path: string) => void;
	onOpen: (hit: SearchHit) => void;
	/** The hit currently open in the centre pane, marked so the reader keeps their place. */
	selected?: { path: string; line?: number } | null;
	label: string;
}) {
	const rows = useMemo(() => searchRows(files, collapsed), [files, collapsed]);
	const scrollRef = useRef<HTMLDivElement | null>(null);
	const virtualizer = useVirtualizer({
		count: rows.length,
		getScrollElement: () => scrollRef.current,
		estimateSize: (index) => (rows[index]?.kind === "file" ? FILE_ROW_HEIGHT : MATCH_ROW_HEIGHT),
		overscan: OVERSCAN,
		observeElementRect: measuredOrFallbackRect,
	});

	return (
		<div className="files-search__list" role="tree" aria-label={label} ref={scrollRef}>
			<div className="files-search__canvas" style={{ height: virtualizer.getTotalSize() }}>
				{virtualizer.getVirtualItems().map((virtualRow) => {
					const row = rows[virtualRow.index];
					// The window is a slice, so each row says where it sits in the whole
					// list; a screen reader counting the DOM would otherwise report
					// "3 of 14" on a 2,000-row result set.
					const seat = { "aria-setsize": rows.length, "aria-posinset": virtualRow.index + 1 };
					const top = virtualRow.start;
					if (row.kind === "file") {
						return (
							<FileHeaderRow
								key={row.key}
								file={row.file}
								collapsed={collapsed.has(row.file.path)}
								onToggle={() => onToggleFile(row.file.path)}
								seat={seat}
								top={top}
							/>
						);
					}
					return (
						<MatchRow
							key={row.key}
							path={row.path}
							match={row.match}
							selected={selected?.path === row.path && selected?.line === row.match.line}
							onOpen={onOpen}
							seat={seat}
							top={top}
						/>
					);
				})}
			</div>
		</div>
	);
}

type Seat = { "aria-setsize": number; "aria-posinset": number };

function FileHeaderRow({
	file,
	collapsed,
	onToggle,
	seat,
	top,
}: {
	file: WorkspaceSearchFile;
	collapsed: boolean;
	onToggle: () => void;
	seat: Seat;
	top: number;
}) {
	const slash = file.path.lastIndexOf("/");
	const name = file.path.slice(slash + 1);
	const dir = slash >= 0 ? file.path.slice(0, slash) : "";
	const KindIcon = KIND_ICON[fileKindFor(file.path)];
	const Chevron = collapsed ? ChevronRight : ChevronDown;
	return (
		<button
			type="button"
			role="treeitem"
			aria-expanded={!collapsed}
			aria-level={1}
			{...seat}
			className="files-search__row files-search__file"
			style={{ top, height: FILE_ROW_HEIGHT }}
			onClick={onToggle}
			title={file.path}
		>
			<Chevron aria-hidden="true" className="files-search__chevron" />
			<KindIcon aria-hidden="true" className="files-search__icon" />
			<span className="files-search__name">
				<bdi>{name}</bdi>
			</span>
			<span className="files-search__dir">
				<bdi>{dir}</bdi>
			</span>
			<span className="files-search__count">{fileCountLabel(file)}</span>
		</button>
	);
}

function MatchRow({
	path,
	match,
	selected,
	onOpen,
	seat,
	top,
}: {
	path: string;
	match: WorkspaceSearchMatch;
	selected: boolean;
	onOpen: (hit: SearchHit) => void;
	seat: Seat;
	top: number;
}) {
	// The indent is trimmed for DISPLAY only — the column that opens the file is
	// untouched, so a deeply indented hit still lands the caret on the match.
	const shown = trimPreviewIndent(match);
	const { before, hit, after } = splitPreview(shown);
	return (
		<button
			type="button"
			role="treeitem"
			aria-level={2}
			{...seat}
			aria-current={selected ? "true" : undefined}
			data-path={path}
			data-line={match.line}
			className={cn("files-search__row files-search__match", selected && "is-selected")}
			style={{ top, height: MATCH_ROW_HEIGHT }}
			onClick={() => onOpen({ path, line: match.line, column: match.column })}
			title={`${path}:${match.line}:${match.column}`}
		>
			<span className="files-search__line">{match.line}</span>
			<span className="files-search__preview">
				{before}
				<mark className="files-search__hit">{hit}</mark>
				{after}
			</span>
		</button>
	);
}

/**
 * The scroller's size, falling back to a nominal viewport when it measures zero
 * — the same treatment FileTree gives it, and for the same reason: jsdom has no
 * layout, so a virtualiser told the truth there renders nothing at all.
 */
const measuredOrFallbackRect: typeof observeElementRect = (instance, cb) =>
	observeElementRect(instance, (rect) =>
		cb(rect.height > 0 ? rect : { width: rect.width || FALLBACK_VIEWPORT.width, height: FALLBACK_VIEWPORT.height }),
	);
