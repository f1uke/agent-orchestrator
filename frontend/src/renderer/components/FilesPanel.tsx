import {
	FileStack,
	FileText,
	FolderOpen,
	GitBranch,
	List,
	ListTree,
	RefreshCw,
	Search,
	TriangleAlert,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { type ChangedFile, useWorkspaceChanges } from "../hooks/useWorkspaceChanges";
import { useWorkspaceFiles } from "../hooks/useWorkspaceFiles";
import { apiErrorMessage } from "../lib/api-client";
import { ancestorKeys, buildFileTree, collapsedExcept, matchesFileQuery, orderedFileItems } from "../lib/file-tree";
import {
	type FilesMode,
	type FilesPanelState,
	type FilesView,
	readFilesPanelState,
	writeFilesPanelState,
	writeGlobalMode,
	writeGlobalView,
} from "../lib/files-panel-state";
import { cn } from "../lib/utils";
import { FileTree } from "./FileTree";
import { Skeleton } from "./ui/skeleton";
import { SimpleTooltip, TooltipProvider } from "./ui/tooltip";

/**
 * What a clicked row opens in the center pane.
 *
 * Every row opens as a DIFF against the target branch, never as a file read.
 * That is deliberate: a deleted file has no working-tree content, so routing
 * rows to the file endpoint would 404 on exactly the rows a reviewer most wants
 * to inspect. Diffing every row avoids the trap structurally instead of
 * special-casing the deleted status.
 */
export type ChangedFileTarget = {
	path: string;
	/**
	 * Carried so the OWNER can route the row. A deleted or binary file has no
	 * working-tree buffer to open, so those rows keep going to the stacked diff
	 * view while everything else opens in the editor. Deciding that here would
	 * hard-code a viewer this panel is deliberately ignorant of.
	 */
	status?: string;
	binary?: boolean;
};

/** A row in Browse mode: any file in the worktree, changed or not. */
export type WorktreeFile = { path: string };

/**
 * How long a scroll rests before it is written down. Scrolling fires per frame;
 * the arrangement is also written on the way out, so this only decides how much
 * of the last scroll survives an app that is killed rather than closed.
 */
const SCROLL_PERSIST_MS = 500;

/**
 * How long the reveal ring stays before clearing. Long enough to find the row
 * after the tab switches, short enough that it never reads as a persistent
 * state — it is a cue, not a marker.
 */
const REVEAL_RING_MS = 1400;

/**
 * Changes mode: the files differing between this session's branch (working tree
 * included) and its target branch, as a folder tree — GitLab's merge-request
 * Changes navigator.
 *
 * The rail runs ~330px by default and never narrower than 280px (SessionView's
 * wrapper pins that min-width so the collapse animation does not reflow), so
 * this panel is a NAVIGATOR, not a viewer. Clicking a row scrolls the center
 * pane's stacked diffs to that file, and the tree highlights whichever file the
 * reader has scrolled to.
 *
 * Tree is the default; the flat list stays available because it is genuinely
 * better for a two-file diff, where a tree only spends indent.
 *
 * BROWSE mode is the same navigator over the whole worktree rather than over
 * the diff, from the file index ⌘⇧O already uses. It shares the search box and
 * the tree/list toggle, because a reader switching between them is asking the
 * same question of a different set.
 */
export function FilesPanel({
	sessionId,
	taskKey = sessionId,
	onOpenFile,
	onOpenWorktreeFile,
	onReviewAll,
	selectedPath,
	reveal,
}: {
	sessionId: string;
	/**
	 * What the arrangement is remembered under — `taskKeyOf(session)`, so dev and
	 * qa, who share one worktree and therefore one tree, share one memory of how
	 * it was left. The owner also mounts this panel KEYED by it, so it never
	 * changes within a mount and the state below can be read once.
	 */
	taskKey?: string;
	onOpenFile?: (target: ChangedFileTarget) => void;
	/** A Browse row. Separate from onOpenFile because it is not a CHANGED file. */
	onOpenWorktreeFile?: (file: WorktreeFile) => void;
	/** The stacked, all-files review. */
	onReviewAll?: () => void;
	selectedPath?: string;
	/**
	 * A file to reveal: expand to it and scroll it in.
	 *
	 * `focus` is the louder form — a clicked terminal reference, which also drops
	 * a search that would hide the row and rings it briefly. Everything else
	 * (`focus` false) FOLLOWS the open file quietly: it never clears what the
	 * reader typed and never rings.
	 */
	reveal?: { path: string; nonce: number; focus?: boolean } | null;
}) {
	// Read once. The owner keys this panel by `taskKey`, so a different task is a
	// different mount rather than a mid-life prop change.
	const [restored] = useState(() => readFilesPanelState(taskKey));
	const [mode, setMode] = useState<FilesMode>(restored.mode);
	const query = useWorkspaceChanges(sessionId);
	const data = query.data;
	// The worktree index is fetched only once Browse is actually chosen: it is a
	// `git ls-files` over the whole tree, and a rail opened on Changes must not
	// pay for it.
	const browse = useWorkspaceFiles(sessionId, mode === "browse");

	const [view, setView] = useState<FilesView>(restored.view);
	// The search box is deliberately NOT restored — see `files-panel-state.ts`.
	const [search, setSearch] = useState("");
	const [collapsedDirs, setCollapsedDirs] = useState<ReadonlySet<string>>(() => new Set(restored.changesCollapsed));
	const [revealedPath, setRevealedPath] = useState<string | null>(null);
	/** The row the tree should bring into view. Bumped, so asking twice scrolls twice. */
	const [scrollTo, setScrollTo] = useState<{ key: string; nonce: number } | null>(null);
	const scrollNonce = useRef(0);
	const listRef = useRef<HTMLDivElement | null>(null);
	// Scroll is kept per MODE: one offset restored into the other mode's tree
	// lands nowhere in particular. A ref, because a scroll must not re-render the
	// list it is scrolling.
	const offsets = useRef({ browse: restored.browseScroll, changes: restored.changesScroll });

	const chooseView = (next: FilesView) => {
		setView(next);
		// Also the habit a task nobody has arranged yet inherits.
		writeGlobalView(next);
	};

	const chooseMode = (next: FilesMode) => {
		setMode(next);
		writeGlobalMode(next);
	};

	const searching = search.trim() !== "";

	const worktreePaths = useMemo(() => browse.data?.paths ?? [], [browse.data]);
	const browseVisible = useMemo(
		() => worktreePaths.filter((p) => matchesFileQuery(p, search)).map((path) => ({ path })),
		[worktreePaths, search],
	);
	const browseTree = useMemo(() => buildFileTree(browseVisible, (f) => f.path), [browseVisible]);
	// Only the view that is actually on screen pays for its ordering. On a 7,000
	// file workspace this call builds a SECOND whole tree — 20ms of it — and it was
	// being spent on every keystroke to order a flat list nobody was looking at.
	const browseOrdered = useMemo(
		() => (view === "list" ? orderedFileItems(browseVisible, (f) => f.path) : []),
		[browseVisible, view],
	);

	const files = useMemo(() => data?.files ?? [], [data]);
	const visible = useMemo(() => files.filter((f) => matchesFileQuery(f.path, search)), [files, search]);
	const tree = useMemo(() => buildFileTree(visible, (f) => f.path), [visible]);
	// The flat list follows the tree's order too, so switching views re-groups the
	// rows without resequencing them — and both match the stacked diffs.
	const ordered = useMemo(() => (view === "list" ? orderedFileItems(visible, (f) => f.path) : []), [visible, view]);

	// Browse's folds, as the set the reader OPENED — the inverse of what Changes
	// keeps, and for the same reason in reverse. Browse opens with everything
	// shut (7,000 files is ~8,500 rows, and a list that long is not navigable
	// however fast it paints), so the small set worth remembering is the handful
	// of folders that were opened. Changes opens fully expanded — it is a diff,
	// every row is a row the reviewer came for — so there it is the handful that
	// were closed.
	const [browseExpanded, setBrowseExpanded] = useState<ReadonlySet<string>>(() => new Set(restored.browseExpanded));
	// Folds made while a search is running live only as long as that query: the
	// results are a different tree each keystroke, and carrying folds across them
	// would hide matches behind directories the reader never closed. Keyed rather
	// than reset in an effect so there is no frame where the wrong set is live.
	const [searchFolds, setSearchFolds] = useState<{ query: string; folds: ReadonlySet<string> }>({
		query: "",
		folds: EMPTY_COLLAPSED,
	});
	const browseFolds = useMemo(() => collapsedExcept(browseTree, browseExpanded), [browseTree, browseExpanded]);
	const browseCollapsed = searching
		? searchFolds.query === search
			? searchFolds.folds
			: EMPTY_COLLAPSED
		: browseFolds;
	const toggleBrowseDir = (key: string) => {
		if (searching) {
			const next = new Set(browseCollapsed);
			if (!next.delete(key)) next.add(key);
			setSearchFolds({ query: search, folds: next });
			return;
		}
		setBrowseExpanded((prev) => {
			const next = new Set(prev);
			if (!next.delete(key)) next.add(key);
			return next;
		});
	};

	const toggleDir = (key: string) =>
		setCollapsedDirs((prev) => {
			const next = new Set(prev);
			if (!next.delete(key)) next.add(key);
			return next;
		});

	// The arrangement, written down. `persist` always closes over the CURRENT
	// render's values, which is what makes the unmount cleanup below correct
	// without listing every piece of state as a dependency.
	//
	// Nothing is written until something actually MOVES: merely glancing at a
	// worker's Files tab would otherwise claim one of the 40 remembered slots and
	// pin that task to whatever mode it happened to open in. Once a task has been
	// written, every later change is written too — including a fold undone back
	// to where it started, which must not leave the stored copy stale.
	const written = useRef(false);
	const persist = useRef<() => void>(() => {});
	persist.current = () => {
		const state = {
			mode,
			view,
			browseExpanded: [...browseExpanded],
			changesCollapsed: [...collapsedDirs],
			browseScroll: offsets.current.browse,
			changesScroll: offsets.current.changes,
		};
		if (!written.current && !arrangementDiffers(state, restored)) return;
		written.current = true;
		writeFilesPanelState(taskKey, state);
	};
	useEffect(() => {
		persist.current();
	}, [mode, view, browseExpanded, collapsedDirs]);
	// Once more on the way out, for the scroll offsets — they move per frame and
	// are never a reason to write on their own.
	useEffect(() => () => persist.current(), []);

	const noteScroll = useCallback((of: "browse" | "changes") => {
		let timer = 0;
		return (offset: number) => {
			offsets.current[of] = offset;
			window.clearTimeout(timer);
			timer = window.setTimeout(() => persist.current(), SCROLL_PERSIST_MS);
		};
	}, []);
	const noteBrowseScroll = useMemo(() => noteScroll("browse"), [noteScroll]);
	const noteChangesScroll = useMemo(() => noteScroll("changes"), [noteScroll]);

	// Reveal, step 1: make the row EXIST. Sharp edges of this panel's state have
	// to be undone first, or the row the tree is asked to scroll to is not
	// rendered at all.
	//
	// 🗝 Ancestors are OPENED and nothing is closed. Revealing a file six levels
	// down means expanding six folders; collapsing the path the reader opened
	// last to "keep the tree tidy" would make it jump under them and throw away
	// the shape they built by hand. Opening a second file elsewhere leaves the
	// first path open — the tree accumulates the shape of the work, and folding
	// is one click. (Xcode's "Reveal in Project Navigator" behaves the same way.)
	//
	// Both fold models are updated, whichever mode is on screen, so switching
	// modes lands on an already-open path rather than a shut one.
	const revealNonce = reveal?.nonce;
	const revealPath = reveal?.path;
	const revealFocus = reveal?.focus ?? false;
	useEffect(() => {
		if (!revealPath) return;
		const ancestors = ancestorKeys(revealPath);
		// The search box filters BEFORE the tree is built, so a target the current
		// query excludes has no row. A terminal reference is explicitly ABOUT where
		// the file is, so it drops the query rather than failing silently; a
		// quiet follow leaves what the reader typed alone and simply does not
		// reveal.
		if (revealFocus) setSearch((prev) => (prev.trim() === "" || matchesFileQuery(revealPath, prev) ? prev : ""));
		// `collapsedDirs` names the CLOSED directories, so OPENING the ancestors
		// means DELETING their keys; `browseExpanded` names the OPEN ones, so it
		// means adding them. Either way the key list is every path PREFIX — see
		// `ancestorKeys` for why that superset is the only form that always
		// contains the real, chain-merged key.
		setCollapsedDirs((prev) => {
			if (prev.size === 0) return prev;
			const next = new Set(prev);
			for (const key of ancestors) next.delete(key);
			return next.size === prev.size ? prev : next;
		});
		setBrowseExpanded((prev) => {
			if (ancestors.every((key) => prev.has(key))) return prev;
			const next = new Set(prev);
			for (const key of ancestors) next.add(key);
			return next;
		});
		scrollNonce.current += 1;
		setScrollTo({ key: revealPath, nonce: scrollNonce.current });
		if (revealFocus) setRevealedPath(revealPath);
	}, [revealPath, revealNonce, revealFocus]);

	// Switching mode or view re-asks for the same row: the tree that just mounted
	// has never been told where the reader was going.
	useEffect(() => {
		if (!revealPath) return;
		scrollNonce.current += 1;
		setScrollTo({ key: revealPath, nonce: scrollNonce.current });
	}, [mode, view, revealPath]);

	// Reveal, step 2, for the FLAT LIST only: the tree scrolls itself by index,
	// because a virtualised row thousands of rows away is not in the DOM to be
	// found. `block: "nearest"` leaves an already-visible row where it is instead
	// of yanking the list. jsdom has no scrollIntoView (test/setup.ts stubs it),
	// so this is guarded exactly like the center pane's viewers.
	const scrollToKey = scrollTo?.key;
	const scrollToNonce = scrollTo?.nonce;
	useEffect(() => {
		if (!scrollToKey || view !== "list") return;
		const row = listRef.current?.querySelector(`[data-path="${CSS.escape(scrollToKey)}"]`);
		if (row instanceof HTMLElement && typeof row.scrollIntoView === "function") {
			row.scrollIntoView({ block: "nearest" });
		}
	}, [scrollToKey, scrollToNonce, view]);

	// The ring is a "look here" cue, not a state: it says where the tree just
	// jumped, then gets out of the way. Holding it would leave a second
	// persistent marker competing with the scroll-spy one.
	useEffect(() => {
		if (!revealedPath) return undefined;
		const timer = window.setTimeout(() => setRevealedPath(null), REVEAL_RING_MS);
		return () => window.clearTimeout(timer);
	}, [revealedPath, revealNonce]);

	return (
		<TooltipProvider delayDuration={0}>
			<div className="files-panel" role="tabpanel">
				<div className="files-panel__modes">
					<div className="files-panel__seg" role="tablist" aria-label="Files mode">
						<button
							type="button"
							role="tab"
							aria-selected={mode === "changes"}
							className={cn("files-panel__seg-btn", mode === "changes" && "is-active")}
							onClick={() => chooseMode("changes")}
						>
							<ListTree aria-hidden="true" className="h-3 w-3 shrink-0" />
							<span className="files-panel__seg-label">Changes</span>
						</button>
						<button
							type="button"
							role="tab"
							aria-selected={mode === "browse"}
							className={cn("files-panel__seg-btn", mode === "browse" && "is-active")}
							onClick={() => chooseMode("browse")}
						>
							<FolderOpen aria-hidden="true" className="h-3 w-3 shrink-0" />
							<span className="files-panel__seg-label">Browse</span>
						</button>
					</div>
				</div>

				{mode === "browse" ? (
					<BrowsePanel
						files={browseVisible}
						tree={browseTree}
						ordered={browseOrdered}
						collapsed={browseCollapsed}
						onToggleDir={toggleBrowseDir}
						revealedKey={revealedPath}
						scrollTo={scrollTo}
						initialScrollOffset={offsets.current.browse}
						onScrollOffsetChange={noteBrowseScroll}
						listRef={listRef}
						total={worktreePaths.length}
						view={view}
						search={search}
						onSearch={setSearch}
						onView={chooseView}
						onOpen={onOpenWorktreeFile}
						selectedPath={selectedPath}
						loading={browse.isPending}
						error={browse.error}
						unavailableReason={browse.data && !browse.data.available ? browse.data.reason : undefined}
						truncated={browse.data?.truncated ?? false}
					/>
				) : null}

				{mode === "changes" && query.isLoading ? <ChangesSkeleton /> : null}

				{mode === "changes" && query.error ? (
					<p className="files-panel__empty-text">{apiErrorMessage(query.error, "Unable to load changes")}</p>
				) : null}

				{mode === "changes" && data && !data.available ? (
					<UnavailableState reason={data.reason} branch={data.targetBranch} />
				) : null}

				{mode === "changes" && data?.available ? (
					<>
						<SummaryLine
							branch={data.targetBranch}
							inferred={data.targetSource === "project" || data.targetSource === "git_origin_head"}
							count={files.length}
							additions={files.reduce((n, f) => n + (f.binary ? 0 : f.additions), 0)}
							deletions={files.reduce((n, f) => n + (f.binary ? 0 : f.deletions), 0)}
							onRefresh={() => void query.refetch()}
							// The spinner covers the daemon's background refresh too, not just
							// this request: while the target branch is being fetched the numbers
							// on screen can still move, and a still icon would claim otherwise.
							refreshing={query.isFetching || data.targetFetch === "refreshing"}
							fetchState={data.targetFetch}
							fetchError={data.targetFetchError}
							onReviewAll={files.length > 0 ? onReviewAll : undefined}
						/>
						{files.length === 0 ? (
							<EmptyState
								icon={<CheckIcon />}
								title={`No changes vs ${data.targetBranch || "target"}`}
								detail="This branch matches its target branch. Nothing to review yet."
							/>
						) : (
							<>
								<Toolbar search={search} onSearch={setSearch} view={view} onView={chooseView} />
								{visible.length === 0 ? (
									<p className="files-panel__truncated">No files match “{search.trim()}”.</p>
								) : (
									<div className="files-panel__list" ref={listRef}>
										{view === "tree" ? (
											<FileTree
												nodes={tree}
												collapsed={collapsedDirs}
												onToggleDir={toggleDir}
												onSelectFile={(f) => onOpenFile?.({ path: f.path, status: f.status, binary: f.binary })}
												selectedKey={selectedPath}
												revealedKey={revealedPath}
												scrollTo={scrollTo}
												initialScrollOffset={offsets.current.changes}
												onScrollOffsetChange={noteChangesScroll}
												label="Changed files"
												getTitle={(f) => f.path}
												getFileLabel={displayName}
												renderLead={(f) => <UncommittedDot file={f} />}
												renderMeta={(f) => <RowMeta file={f} />}
											/>
										) : (
											<div role="listbox" aria-label="Changed files" className="files-panel__flat">
												{ordered.map((file) => (
													<ChangedFileRow
														key={file.path}
														file={file}
														selected={file.path === selectedPath}
														revealed={file.path === revealedPath}
														onOpen={onOpenFile}
													/>
												))}
											</div>
										)}
										{data.truncated ? (
											<p className="files-panel__truncated">
												Showing the first {files.length} files — the diff is larger.
											</p>
										) : null}
									</div>
								)}
							</>
						)}
					</>
				) : null}
			</div>
		</TooltipProvider>
	);
}

function Toolbar({
	search,
	onSearch,
	view,
	onView,
}: {
	search: string;
	onSearch: (value: string) => void;
	view: FilesView;
	onView: (view: FilesView) => void;
}) {
	return (
		<div className="files-panel__toolbar">
			<span className="files-panel__search">
				<Search aria-hidden="true" className="files-panel__search-icon" />
				<input
					type="search"
					role="searchbox"
					aria-label="Search changed files"
					placeholder="Search (e.g. *.vue)"
					className="files-panel__search-input"
					value={search}
					onChange={(e) => onSearch(e.target.value)}
				/>
			</span>
			<span className="files-panel__view-toggle">
				<ViewButton label="Tree view" active={view === "tree"} onClick={() => onView("tree")}>
					<ListTree aria-hidden="true" className="h-3.5 w-3.5" />
				</ViewButton>
				<ViewButton label="List view" active={view === "list"} onClick={() => onView("list")}>
					<List aria-hidden="true" className="h-3.5 w-3.5" />
				</ViewButton>
			</span>
		</div>
	);
}

function ViewButton({
	label,
	active,
	onClick,
	children,
}: {
	label: string;
	active: boolean;
	onClick: () => void;
	children: React.ReactNode;
}) {
	return (
		<SimpleTooltip label={label}>
			<button
				type="button"
				aria-label={label}
				aria-pressed={active}
				className={cn("files-panel__view-btn", active && "is-active")}
				onClick={onClick}
			>
				{children}
			</button>
		</SimpleTooltip>
	);
}

/** Glyph inside the trailing status box, mirroring GitLab's own set. */
const STATUS_GLYPH: Record<string, string> = {
	added: "+",
	modified: "●",
	deleted: "−",
	renamed: "→",
};

const STATUS_TITLE: Record<string, string> = {
	added: "Added",
	modified: "Modified",
	deleted: "Deleted",
	renamed: "Renamed",
};

/** Our own signal, not GitLab's: this file's change is not committed yet. */
function UncommittedDot({ file }: { file: ChangedFile }) {
	if (file.committed) return null;
	return <span aria-label="uncommitted" className="files-panel__uncommitted" title="Uncommitted changes" />;
}

/**
 * GitLab's trailing status box — a bordered square carrying `+`, `●`, `−` or
 * `→` — rather than a leading letter, so the eye scans filenames down a clean
 * left edge and picks up status on the right.
 */
function StatusBadge({ file }: { file: ChangedFile }) {
	const status = file.status || "modified";
	return (
		<span
			className={cn("files-panel__status", `is-${status}`)}
			title={STATUS_TITLE[status] ?? "Modified"}
			aria-label={STATUS_TITLE[status] ?? "Modified"}
			role="img"
		>
			<span aria-hidden="true">{STATUS_GLYPH[status] ?? "●"}</span>
		</span>
	);
}

/** Counts then status box — the trailing cluster shared by both views. */
function RowMeta({ file, className }: { file: ChangedFile; className?: string }) {
	return (
		<span className={cn("files-panel__meta", className)}>
			<Counts file={file} />
			<StatusBadge file={file} />
		</span>
	);
}

/**
 * Renamed files read `old → new`, in whichever view they appear. `label` is what
 * the row would otherwise show — the bare basename in the flat list, or a merged
 * path fragment in the tree.
 */
function displayName(file: ChangedFile, label = file.path.slice(file.path.lastIndexOf("/") + 1)): string {
	if (!file.oldPath) return label;
	return `${file.oldPath.slice(file.oldPath.lastIndexOf("/") + 1)} → ${label}`;
}

function ChangedFileRow({
	file,
	selected,
	revealed,
	onOpen,
}: {
	file: ChangedFile;
	selected: boolean;
	revealed: boolean;
	onOpen?: (target: ChangedFileTarget) => void;
}) {
	const slash = file.path.lastIndexOf("/");
	const dir = slash >= 0 ? file.path.slice(0, slash) : "";

	return (
		<button
			type="button"
			role="option"
			aria-selected={selected}
			data-path={file.path}
			aria-current={selected ? "true" : undefined}
			className={cn("files-panel__row", selected && "is-selected", revealed && "is-revealed")}
			onClick={() => onOpen?.({ path: file.path, status: file.status, binary: file.binary })}
			title={file.path}
		>
			<span className="files-panel__lead">
				<UncommittedDot file={file} />
			</span>
			<span className="files-panel__name">
				<bdi>{displayName(file)}</bdi>
			</span>
			{/* One counts element placed by the row grid, rather than a second copy
			    on the wrapped line — duplicate text would be announced twice by
			    assistive tech whenever the stylesheet failed to load. */}
			<RowMeta file={file} className="files-panel__counts" />
			<span className="files-panel__dir">
				<bdi>{dir}</bdi>
			</span>
		</button>
	);
}

function Counts({ file, className }: { file: ChangedFile; className?: string }) {
	// git emits "-" counts for a binary file; rendering them arithmetically
	// produces a nonsense "+0 −0".
	if (file.binary) {
		return <span className={cn(className, "files-panel__counts--binary")}>bin</span>;
	}
	return (
		<span className={className}>
			<span className="files-panel__add">+{file.additions}</span>{" "}
			<span className="files-panel__del">−{file.deletions}</span>
		</span>
	);
}

function SummaryLine({
	branch,
	inferred,
	count,
	additions,
	deletions,
	onRefresh,
	refreshing,
	fetchState,
	fetchError,
	onReviewAll,
}: {
	branch?: string;
	inferred: boolean;
	count: number;
	additions: number;
	deletions: number;
	onRefresh: () => void;
	refreshing: boolean;
	fetchState?: string;
	fetchError?: string;
	/**
	 * The stacked, all-files review. It lives here because a ROW now opens the
	 * editor on that file's first hunk, and reading every file in sequence is a
	 * different job from working on one — losing its only entry point would have
	 * been a regression hidden inside an addition.
	 */
	onReviewAll?: () => void;
}) {
	return (
		<div className="files-panel__summary">
			<span
				className="files-panel__vs"
				title={inferred ? `Comparing against ${branch} (inferred)` : `Comparing against ${branch}`}
			>
				vs {branch}
				{inferred ? <span className="files-panel__inferred">*</span> : null}
			</span>
			<StaleMarker branch={branch} state={fetchState} error={fetchError} />
			<span className="files-panel__sep">·</span>
			<span className="files-panel__count">
				{count} {count === 1 ? "file" : "files"}
			</span>
			<span className="files-panel__totals">
				<span className="files-panel__add">+{additions}</span> <span className="files-panel__del">−{deletions}</span>
			</span>
			{onReviewAll ? (
				<SimpleTooltip label="Read every changed file in one scroll">
					<button
						type="button"
						aria-label="Review all changed files"
						className="files-panel__refresh"
						onClick={onReviewAll}
					>
						<FileStack aria-hidden="true" className="h-3 w-3" />
					</button>
				</SimpleTooltip>
			) : null}
			<button
				type="button"
				aria-label="Refresh changes"
				title="Refresh"
				className="files-panel__refresh"
				onClick={onRefresh}
			>
				<RefreshCw aria-hidden="true" className={cn("h-3 w-3", refreshing && "animate-spin")} />
			</button>
		</div>
	);
}

/**
 * The freshness marker on the comparison branch.
 *
 * A diff measured against a ref nobody could refresh looks exactly like a
 * correct one, so "could not refresh" has to be visible or the panel is
 * confidently wrong. Only the degraded state is marked: a successful refresh is
 * the expectation, and a badge saying "current" on every render would be noise
 * that trains the eye to skip the badge that matters. Nothing is shown for a
 * repository with no remote either — it has nothing to be behind.
 */
function StaleMarker({ branch, state, error }: { branch?: string; state?: string; error?: string }) {
	if (state !== "failed") return null;
	const target = branch || "the target branch";
	return (
		<SimpleTooltip
			label={
				// Width-capped here rather than on the shared TooltipContent: git's
				// message is one long unbroken line (a remote URL or a filesystem
				// path), so an uncapped bubble stretches clear across the window.
				<span className="files-panel__stale-tip">
					Could not refresh {target} from origin, so this diff may be out of date.
					{error ? <span className="files-panel__stale-detail">{error}</span> : null}
				</span>
			}
		>
			<span className="files-panel__stale" role="status" aria-label={`Could not refresh ${target}`}>
				<TriangleAlert aria-hidden="true" className="h-3 w-3" />
			</span>
		</SimpleTooltip>
	);
}

function UnavailableState({ reason, branch }: { reason?: string; branch?: string }) {
	if (reason === "no_workspace") {
		return (
			<EmptyState
				icon={<FolderOpen aria-hidden="true" className="h-6 w-6" />}
				title="Worktree no longer on disk"
				detail="This session's worktree was cleaned up. Its diff lives on the pull request."
			/>
		);
	}
	if (reason === "not_a_repo") {
		return (
			<EmptyState
				icon={<FileText aria-hidden="true" className="h-6 w-6" />}
				title="Not a git repository"
				detail="This session's workspace is not a git repository, so there is nothing to diff."
			/>
		);
	}
	// no_target_branch — deliberately never guesses "main": a wrong target
	// renders a confidently wrong diff.
	return (
		<EmptyState
			icon={<GitBranch aria-hidden="true" className="h-6 w-6" />}
			title="No target branch to compare"
			detail={
				branch
					? `This session names ${branch} as its target, but that branch does not exist in this worktree.`
					: "This session has no PR and the project has no default branch set, so there is nothing to diff against."
			}
		/>
	);
}

function EmptyState({ icon, title, detail }: { icon: React.ReactNode; title: string; detail: string }) {
	return (
		<div className="files-panel__empty">
			<span className="files-panel__empty-icon">{icon}</span>
			<span className="files-panel__empty-title">{title}</span>
			<span className="files-panel__empty-text">{detail}</span>
		</div>
	);
}

function CheckIcon() {
	return (
		<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" className="h-6 w-6" aria-hidden="true">
			<path d="M20 6 9 17l-5-5" />
		</svg>
	);
}

function ChangesSkeleton() {
	return (
		<div className="files-panel__list" aria-hidden="true">
			{[0, 1, 2, 3].map((i) => (
				<div key={i} className="files-panel__row">
					<span className="files-panel__row-main">
						<Skeleton className="h-3 w-3 rounded-sm" />
						<Skeleton className="h-3 flex-1" />
					</span>
					<span className="files-panel__row-sub">
						<Skeleton className="h-2.5 w-2/3" />
					</span>
				</div>
			))}
		</div>
	);
}

/**
 * Browse mode: the whole worktree as a tree, from the same file index ⌘⇧O uses.
 *
 * It shares the search box and the tree/list toggle with Changes deliberately —
 * a reader switching modes is asking the same question of a different set, and
 * a second, differently-shaped control for the same job is how a rail becomes
 * two rails. What it does NOT share is the counts and the status box: an
 * unchanged file has neither, and inventing a `+0 −0` for it would be noise
 * that trains the eye to stop reading the column that matters in Changes.
 */
function BrowsePanel({
	files,
	tree,
	ordered,
	collapsed,
	onToggleDir,
	revealedKey,
	scrollTo,
	initialScrollOffset,
	onScrollOffsetChange,
	listRef,
	total,
	view,
	search,
	onSearch,
	onView,
	onOpen,
	selectedPath,
	loading,
	error,
	unavailableReason,
	truncated,
}: {
	files: readonly WorktreeFile[];
	tree: ReturnType<typeof buildFileTree<WorktreeFile>>;
	ordered: readonly WorktreeFile[];
	collapsed: ReadonlySet<string>;
	onToggleDir: (key: string) => void;
	revealedKey?: string | null;
	scrollTo?: { key: string; nonce: number } | null;
	initialScrollOffset?: number;
	onScrollOffsetChange?: (offset: number) => void;
	/** The flat list's scroller, so the owner's reveal can find a row in it. */
	listRef?: React.RefObject<HTMLDivElement | null>;
	total: number;
	view: FilesView;
	search: string;
	onSearch: (value: string) => void;
	onView: (view: FilesView) => void;
	onOpen?: (file: WorktreeFile) => void;
	selectedPath?: string;
	loading: boolean;
	error: unknown;
	unavailableReason?: string;
	truncated: boolean;
}) {
	if (loading) return <ChangesSkeleton />;
	if (error)
		return <p className="files-panel__empty-text">{apiErrorMessage(error, "Unable to index this workspace")}</p>;
	if (unavailableReason) return <UnavailableState reason={unavailableReason} />;
	if (total === 0) {
		return (
			<EmptyState
				icon={<FolderOpen aria-hidden="true" className="h-6 w-6" />}
				title="Nothing to browse"
				detail="This session's workspace has no files the index could reach."
			/>
		);
	}

	return (
		<>
			<div className="files-panel__summary">
				<span className="files-panel__count">
					{total} {total === 1 ? "file" : "files"}
				</span>
			</div>
			<Toolbar search={search} onSearch={onSearch} view={view} onView={onView} />
			{files.length === 0 ? (
				<p className="files-panel__truncated">No files match “{search.trim()}”.</p>
			) : (
				<div className="files-panel__list" ref={listRef}>
					{view === "tree" ? (
						<FileTree
							nodes={tree}
							collapsed={collapsed}
							onToggleDir={onToggleDir}
							onSelectFile={(f) => onOpen?.(f)}
							selectedKey={selectedPath}
							revealedKey={revealedKey}
							scrollTo={scrollTo}
							initialScrollOffset={initialScrollOffset}
							onScrollOffsetChange={onScrollOffsetChange}
							label="Workspace files"
							getTitle={(f) => f.path}
						/>
					) : (
						<div role="listbox" aria-label="Workspace files" className="files-panel__flat">
							{ordered.map((file) => (
								<button
									key={file.path}
									type="button"
									role="option"
									aria-selected={file.path === selectedPath}
									data-path={file.path}
									className={cn(
										"files-panel__row",
										file.path === selectedPath && "is-selected",
										file.path === revealedKey && "is-revealed",
									)}
									onClick={() => onOpen?.(file)}
									title={file.path}
								>
									<span className="files-panel__name">
										<bdi>{file.path.slice(file.path.lastIndexOf("/") + 1)}</bdi>
									</span>
									<span className="files-panel__dir">
										<bdi>{file.path.slice(0, Math.max(0, file.path.lastIndexOf("/")))}</bdi>
									</span>
								</button>
							))}
						</div>
					)}
					{truncated ? (
						<p className="files-panel__truncated">Showing the first {total} files — the workspace is larger.</p>
					) : null}
				</div>
			)}
		</>
	);
}

/** Nothing folded — what a searching tree passes, so no match can hide. */
const EMPTY_COLLAPSED: ReadonlySet<string> = new Set();

/** Has anything about the arrangement moved since it was restored? */
function arrangementDiffers(next: FilesPanelState, restored: FilesPanelState): boolean {
	return (
		next.mode !== restored.mode ||
		next.view !== restored.view ||
		next.browseScroll !== restored.browseScroll ||
		next.changesScroll !== restored.changesScroll ||
		next.browseExpanded.join("\n") !== restored.browseExpanded.join("\n") ||
		next.changesCollapsed.join("\n") !== restored.changesCollapsed.join("\n")
	);
}
