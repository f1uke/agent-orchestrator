import { FileText, FolderOpen, GitBranch, List, ListTree, RefreshCw, Search } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { type ChangedFile, useWorkspaceChanges } from "../hooks/useWorkspaceChanges";
import { apiErrorMessage } from "../lib/api-client";
import { buildFileTree, matchesFileQuery, orderedFileItems } from "../lib/file-tree";
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
export type ChangedFileTarget = { path: string };

type FilesView = "tree" | "list";

const VIEW_STORAGE_KEY = "ao.files.view";

function storedView(): FilesView {
	try {
		return window.localStorage?.getItem(VIEW_STORAGE_KEY) === "list" ? "list" : "tree";
	} catch {
		// Private-mode / disabled storage must not take the panel down with it.
		return "tree";
	}
}

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
 * Browse mode ships separately; its segment is present but disabled so the
 * control does not change shape when it lands.
 */
export function FilesPanel({
	sessionId,
	onOpenFile,
	selectedPath,
	reveal,
}: {
	sessionId: string;
	onOpenFile?: (target: ChangedFileTarget) => void;
	selectedPath?: string;
	/** A terminal reference to reveal: expand to it, scroll it in, ring it briefly. */
	reveal?: { path: string; nonce: number } | null;
}) {
	const query = useWorkspaceChanges(sessionId);
	const data = query.data;

	const [view, setView] = useState<FilesView>(storedView);
	const [search, setSearch] = useState("");
	const [collapsedDirs, setCollapsedDirs] = useState<ReadonlySet<string>>(() => new Set());
	const [revealedPath, setRevealedPath] = useState<string | null>(null);
	const listRef = useRef<HTMLDivElement | null>(null);

	const chooseView = (next: FilesView) => {
		setView(next);
		try {
			window.localStorage?.setItem(VIEW_STORAGE_KEY, next);
		} catch {
			// Preference is a nicety; failing to persist it must not break the view.
		}
	};

	const files = useMemo(() => data?.files ?? [], [data]);
	const visible = useMemo(() => files.filter((f) => matchesFileQuery(f.path, search)), [files, search]);
	const tree = useMemo(() => buildFileTree(visible, (f) => f.path), [visible]);
	// The flat list follows the tree's order too, so switching views re-groups the
	// rows without resequencing them — and both match the stacked diffs.
	const ordered = useMemo(() => orderedFileItems(visible, (f) => f.path), [visible]);

	const toggleDir = (key: string) =>
		setCollapsedDirs((prev) => {
			const next = new Set(prev);
			if (!next.delete(key)) next.add(key);
			return next;
		});

	// Reveal, step 1: make the row EXIST. Two sharp edges of this panel's state
	// have to be undone first, or the row the next effect scrolls to is not
	// rendered at all.
	const revealNonce = reveal?.nonce;
	const revealPath = reveal?.path;
	useEffect(() => {
		if (!revealPath) return;
		// The search box filters BEFORE the tree is built, so a target the current
		// query excludes has no row. Clear the query rather than fail silently.
		setSearch((prev) => (prev.trim() === "" || matchesFileQuery(revealPath, prev) ? prev : ""));
		// `collapsedDirs` names the CLOSED directories, so OPENING the ancestors
		// means DELETING their keys. Directory keys are post-chain-merge — an
		// only-child chain a/b/c collapses to a single row keyed "a/b/c", not "a" —
		// so deleting every path prefix is a superset that always contains the real
		// key, and the prefixes that name no row are harmless no-ops.
		setCollapsedDirs((prev) => {
			if (prev.size === 0) return prev;
			const parts = revealPath.split("/");
			const next = new Set(prev);
			for (let i = 1; i < parts.length; i++) next.delete(parts.slice(0, i).join("/"));
			return next.size === prev.size ? prev : next;
		});
		setRevealedPath(revealPath);
	}, [revealPath, revealNonce]);

	// Reveal, step 2: scroll to the row, now that step 1's state has rendered.
	// Keyed on the nonce too, so clicking the same reference twice re-scrolls.
	// `block: "nearest"` leaves an already-visible row where it is instead of
	// yanking the list. jsdom has no scrollIntoView (test/setup.ts stubs it), so
	// this is guarded exactly like the center pane's viewers.
	useEffect(() => {
		if (!revealedPath) return;
		const row = listRef.current?.querySelector(`[data-path="${CSS.escape(revealedPath)}"]`);
		if (row instanceof HTMLElement && typeof row.scrollIntoView === "function") {
			row.scrollIntoView({ block: "nearest" });
		}
	}, [revealedPath, revealNonce, view]);

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
						<button type="button" role="tab" aria-selected="true" className="files-panel__seg-btn is-active">
							<ListTree aria-hidden="true" className="h-3 w-3 shrink-0" />
							<span className="files-panel__seg-label">Changes</span>
						</button>
						<SimpleTooltip label="Browsing the whole worktree ships separately">
							{/* A disabled button emits no pointer events, so the tooltip needs a
						    wrapper to hover. */}
							<span className="files-panel__seg-slot">
								<button type="button" role="tab" aria-selected="false" disabled className="files-panel__seg-btn">
									<FolderOpen aria-hidden="true" className="h-3 w-3 shrink-0" />
									<span className="files-panel__seg-label">Browse</span>
								</button>
							</span>
						</SimpleTooltip>
					</div>
				</div>

				{query.isLoading ? <ChangesSkeleton /> : null}

				{query.error ? (
					<p className="files-panel__empty-text">{apiErrorMessage(query.error, "Unable to load changes")}</p>
				) : null}

				{data && !data.available ? <UnavailableState reason={data.reason} branch={data.targetBranch} /> : null}

				{data?.available ? (
					<>
						<SummaryLine
							branch={data.targetBranch}
							inferred={data.targetSource === "project" || data.targetSource === "git_origin_head"}
							count={files.length}
							additions={files.reduce((n, f) => n + (f.binary ? 0 : f.additions), 0)}
							deletions={files.reduce((n, f) => n + (f.binary ? 0 : f.deletions), 0)}
							onRefresh={() => void query.refetch()}
							refreshing={query.isFetching}
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
												onSelectFile={(f) => onOpenFile?.({ path: f.path })}
												selectedKey={selectedPath}
												revealedKey={revealedPath}
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
			onClick={() => onOpen?.({ path: file.path })}
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
}: {
	branch?: string;
	inferred: boolean;
	count: number;
	additions: number;
	deletions: number;
	onRefresh: () => void;
	refreshing: boolean;
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
			<span className="files-panel__sep">·</span>
			<span className="files-panel__count">
				{count} {count === 1 ? "file" : "files"}
			</span>
			<span className="files-panel__totals">
				<span className="files-panel__add">+{additions}</span> <span className="files-panel__del">−{deletions}</span>
			</span>
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
