import { observeElementRect, useVirtualizer } from "@tanstack/react-virtual";
import { File, FileCode2, FileCog, FileImage, FileJson2, FileText, FileType2, Folder, FolderOpen } from "lucide-react";
import { type ReactNode, useEffect, useLayoutEffect, useMemo, useRef } from "react";
import { type FileKind, fileKindFor } from "../lib/file-kind";
import { type FileTreeNode, flattenFileTree } from "../lib/file-tree";
import { cn } from "../lib/utils";

/** One icon per coarse file bucket — see `fileKindFor` for why not per language. */
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
 * Row height, in px. Pinned in CSS (`.file-tree__row`) rather than left to the
 * content, so this number is EXACT rather than an estimate: 7,000 files is 8,500
 * rows, and a per-row error of even half a pixel — which is what the content-sized
 * rows measured, 29.75px for some and 30.5px for others — accumulates into
 * thousands of pixels of scrollbar drift down a tree that size.
 */
const ROW_HEIGHT = 30;

/**
 * How many rows to render beyond the viewport. Enough that a fast flick does not
 * expose blank rows, small enough that the DOM stays a viewport's worth.
 */
const OVERSCAN = 10;

/**
 * The viewport a tree assumes when its scroller measures zero-height.
 *
 * jsdom has no layout, so `getBoundingClientRect()` there is all zeros and a
 * virtualiser told the truth would render nothing at all — every existing tree
 * test would go blank. It is also the right answer in the browser for the moment
 * before layout has run: rows appear immediately and the ResizeObserver corrects
 * the count on the next frame.
 */
const FALLBACK_VIEWPORT = { width: 320, height: 900 };

const INDENT_BASE = 8;
/** Indent per level, and the half-step levels past `TAPER_AFTER` fall back to. */
const INDENT_STEP = 11;
const INDENT_STEP_DEEP = 6;
const TAPER_AFTER = 4;

/**
 * Left offset of a row at `depth`.
 *
 * The step NARROWS past the fourth level rather than stopping: a rail whose
 * content floor is 280px cannot spend 11px a level forever, but a level that
 * costs nothing is a level the reader cannot see. Every level moves, so a file
 * is never drawn at its parent's x — the thing the tree exists to say. The
 * first four levels keep the full step, so the shallow trees that are the
 * common case render exactly as before.
 *
 * Nothing overflows the rail as depth grows: the row is a flex line whose name
 * cell truncates (`.file-tree__name`), so depth costs name width, never layout.
 */
function indentFor(depth: number): number {
	return INDENT_BASE + Math.min(depth, TAPER_AFTER) * INDENT_STEP + Math.max(0, depth - TAPER_AFTER) * INDENT_STEP_DEEP;
}

/**
 * A collapsible folder tree over any item type.
 *
 * Generic and payload-agnostic by design: Changes mode supplies changed files
 * and a ±counts meta column, and Browse mode (the full worktree tree, shipping
 * separately) is meant to drop straight in with its own items and renderers
 * rather than grow a second tree.
 *
 * Indent tapers with depth rather than stopping (see `indentFor`), and
 * single-child directory chains are already merged upstream in `buildFileTree`,
 * so most trees never reach the narrow steps at all.
 *
 * VIRTUALISED: it renders the rows in view and a little either side, never the
 * whole tree. A real 7,000-file iOS project flattens to ~8,500 rows and ~91,000
 * DOM nodes — the indent hairlines alone are one node per ancestor per row — which
 * measured at 568 ms to first paint and 292 ms of blocked main thread on EVERY
 * keystroke in the filter box. Windowing is what makes the rail usable at that
 * size; the tree model above it is cheap (~40 ms) and was never the problem.
 *
 * It owns its own scroll container for that reason: a virtualiser needs the
 * scrolling element, and passing a ref up into the panel would make every caller
 * responsible for a detail that belongs to the tree.
 */
export function FileTree<T>({
	nodes,
	collapsed,
	onToggleDir,
	onSelectFile,
	selectedKey,
	revealedKey,
	scrollTo,
	initialScrollOffset,
	onScrollOffsetChange,
	label,
	renderLead,
	renderMeta,
	getFileKey,
	getFileLabel,
	getTitle,
}: {
	nodes: readonly FileTreeNode<T>[];
	/**
	 * Key of a file just revealed from a terminal reference. Transient (the owner
	 * clears it), and styled as a RING rather than a fill so it stays legible
	 * against — and distinct from — `selectedKey`, which is the reader's
	 * scroll-spy position and owns the accent left bar + fill.
	 */
	revealedKey?: string | null;
	/**
	 * A row to bring into view. The nonce is what makes asking twice for the same
	 * row scroll twice — clicking a terminal reference again, or re-selecting the
	 * file that is already open.
	 *
	 * Separate from `revealedKey` because scrolling and RINGING are different
	 * jobs: following the open file scrolls silently, while a terminal reference
	 * rings as well.
	 */
	scrollTo?: { key: string; nonce: number } | null;
	/**
	 * Where this tree was scrolled to last time it was on screen. Applied once,
	 * on mount, before any `scrollTo` — so a reveal always wins over a restore.
	 */
	initialScrollOffset?: number;
	/** Reports the scroll offset so the owner can remember it. */
	onScrollOffsetChange?: (offset: number) => void;
	/** Keys of the directories that are CLOSED; everything else is open. */
	collapsed: ReadonlySet<string>;
	onToggleDir: (key: string) => void;
	onSelectFile?: (item: T) => void;
	/** Key of the file row to mark as current. */
	selectedKey?: string;
	label: string;
	renderLead?: (item: T) => ReactNode;
	renderMeta?: (item: T) => ReactNode;
	/** Overrides the node key used for selection/`data-path` (defaults to the path). */
	getFileKey?: (item: T) => string;
	/**
	 * Decorates a file row's text. Receives the tree's own label — which for a
	 * merged single-child chain is a path fragment, not a bare basename — so the
	 * caller can extend it rather than replace it. Changes mode uses it to render
	 * a rename as `old → new`.
	 */
	getFileLabel?: (item: T, label: string) => string;
	getTitle?: (item: T) => string;
}) {
	const rows = useMemo(() => flattenFileTree(nodes, collapsed), [nodes, collapsed]);
	const scrollRef = useRef<HTMLDivElement | null>(null);
	const virtualizer = useVirtualizer({
		count: rows.length,
		getScrollElement: () => scrollRef.current,
		estimateSize: () => ROW_HEIGHT,
		overscan: OVERSCAN,
		observeElementRect: measuredOrFallbackRect,
	});

	// Restoring where this tree was left, before anything asks it to scroll
	// somewhere else. A layout effect so the rows never paint at the top and then
	// jump, and mount-only: later scrolling is the reader's.
	const restoreRef = useRef(initialScrollOffset ?? 0);
	useLayoutEffect(() => {
		const el = scrollRef.current;
		if (el && restoreRef.current > 0) el.scrollTop = restoreRef.current;
	}, []);

	// A row asked for can be thousands of rows outside the rendered WINDOW, where
	// a `scrollIntoView` on a looked-up node has no element to find — so the tree
	// scrolls to it by INDEX instead. `scrollToIndex` is what makes reveal work at
	// all once windowing is on, which is the reason this is not hand-rolled.
	const scrollKey = scrollTo?.key;
	const scrollNonce = scrollTo?.nonce;
	const scrollIndex = scrollKey == null ? -1 : rows.findIndex((r) => rowKey(r.node, getFileKey) === scrollKey);
	useEffect(() => {
		if (scrollIndex >= 0) virtualizer.scrollToIndex(scrollIndex, { align: "auto" });
		// `scrollNonce` is a dep so asking twice for the same row scrolls twice.
	}, [scrollIndex, scrollNonce, virtualizer]);

	return (
		<div
			className="file-tree"
			role="tree"
			aria-label={label}
			ref={scrollRef}
			onScroll={onScrollOffsetChange ? (e) => onScrollOffsetChange(e.currentTarget.scrollTop) : undefined}
		>
			<div className="file-tree__canvas" style={{ height: virtualizer.getTotalSize() }}>
				{virtualizer.getVirtualItems().map((virtualRow) => {
					const { node, depth, expanded } = rows[virtualRow.index];
					const indent = indentFor(depth);
					const guides = <IndentGuides depth={depth} />;
					// Rows are absolutely placed by the virtualiser, so the only thing
					// keeping the list in order is `top` — never source order.
					const position = { top: virtualRow.start, height: ROW_HEIGHT, paddingLeft: indent };
					// The window is a slice of the tree, so the row has to SAY where it
					// sits in the whole thing; a screen reader counting the DOM would
					// otherwise report "3 of 14" on a 8,500-row tree.
					const seat = { "aria-setsize": rows.length, "aria-posinset": virtualRow.index + 1 };
					if (node.kind === "dir") {
						return (
							<button
								key={node.key}
								type="button"
								role="treeitem"
								aria-expanded={expanded}
								aria-level={depth + 1}
								{...seat}
								className="file-tree__row file-tree__row--dir"
								style={position}
								onClick={() => onToggleDir(node.key)}
								title={node.key}
							>
								{guides}
								{/* An open/closed folder carries the expansion state on its own, the
								    way GitLab's tree does — no separate chevron column to pay for. */}
								{expanded ? (
									<FolderOpen aria-hidden="true" className="file-tree__icon file-tree__icon--folder" />
								) : (
									<Folder aria-hidden="true" className="file-tree__icon file-tree__icon--folder" />
								)}
								<span className="file-tree__dir-label">
									<bdi>{node.label}</bdi>
								</span>
							</button>
						);
					}
					const kind = fileKindFor(node.key);
					const KindIcon = KIND_ICON[kind];
					const key = getFileKey ? getFileKey(node.item) : node.key;
					const selected = selectedKey != null && key === selectedKey;
					return (
						<button
							key={node.key}
							type="button"
							role="treeitem"
							aria-level={depth + 1}
							{...seat}
							aria-current={selected ? "true" : undefined}
							data-path={key}
							className={cn(
								"file-tree__row file-tree__row--file",
								selected && "is-selected",
								revealedKey != null && key === revealedKey && "is-revealed",
							)}
							style={position}
							onClick={() => onSelectFile?.(node.item)}
							title={getTitle ? getTitle(node.item) : node.key}
						>
							{guides}
							<KindIcon aria-hidden="true" className={cn("file-tree__icon", `file-tree__icon--${kind}`)} />
							{renderLead ? <span className="file-tree__lead">{renderLead(node.item)}</span> : null}
							<span className="file-tree__name">
								<bdi>{getFileLabel ? getFileLabel(node.item, node.label) : node.label}</bdi>
							</span>
							{renderMeta ? <span className="file-tree__meta">{renderMeta(node.item)}</span> : null}
						</button>
					);
				})}
			</div>
		</div>
	);
}

/** The key a row is addressed by — the caller's override, or the node's path. */
function rowKey<T>(node: FileTreeNode<T>, getFileKey?: (item: T) => string): string {
	return node.kind === "file" && getFileKey ? getFileKey(node.item) : node.key;
}

/**
 * The scroller's size, falling back to a nominal viewport when it measures zero.
 *
 * `observeElementRect` is the library's own implementation; all this adds is the
 * floor. See FALLBACK_VIEWPORT for why a zero-height scroller must not mean an
 * empty tree.
 */
const measuredOrFallbackRect: typeof observeElementRect = (instance, cb) =>
	observeElementRect(instance, (rect) =>
		cb(rect.height > 0 ? rect : { width: rect.width || FALLBACK_VIEWPORT.width, height: FALLBACK_VIEWPORT.height }),
	);

/**
 * The hairlines that trace each open ancestor down the rows beneath it, as
 * GitLab's tree draws them. Purely decorative — depth is already exposed to
 * assistive tech via `aria-level`.
 *
 * One per ancestor at every depth, positioned by the same `indentFor` the rows
 * use — a guide that stopped where the indent tapers would leave the deepest
 * levels the only ones untraced. They REINFORCE the indent rather than replace
 * it: measured against their surface these hairlines sit at 1.13:1 (dark) and
 * 1.25:1 (light), so the row's own offset has to carry the nesting on its own,
 * which is why the taper keeps a half-step instead of shrinking toward zero.
 */
function IndentGuides({ depth }: { depth: number }) {
	if (depth === 0) return null;
	return (
		<>
			{Array.from({ length: depth }, (_, i) => (
				<span key={i} aria-hidden="true" className="file-tree__guide" style={{ left: indentFor(i) + 5 }} />
			))}
		</>
	);
}
