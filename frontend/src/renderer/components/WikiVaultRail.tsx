import { useCallback, useMemo, useState } from "react";
import { BookText, ChevronDown, ChevronRight, RefreshCw, Search } from "lucide-react";
import type { WikiFiles } from "../hooks/useWiki";
import { defaultOpen, loadFolderState, saveFolderState, type WikiFolderState } from "../lib/wiki-tree-state";

/**
 * The vault, on the right of the Wiki page: every file in it, as a folder tree.
 *
 * The tab strip reuses the inspector rail's OWN classes
 * (`.session-inspector__tabs` / `__tab`) rather than restating their metrics,
 * so the two rails cannot drift apart.
 *
 * It keeps working with no agent running — reading your notes never needed one.
 */

type TreeFolder = {
	kind: "folder";
	name: string;
	path: string;
	children: TreeNode[];
	/** Files anywhere beneath this folder, which is what the count means. */
	noteCount: number;
};

type TreeFile = {
	kind: "file";
	name: string;
	path: string;
	modifiedAt?: string;
};

type TreeNode = TreeFolder | TreeFile;

export function WikiVaultRail({
	files,
	loading,
	openPath,
	onOpenNote,
	onRefresh,
	query,
	onQueryChange,
}: {
	files: WikiFiles | undefined;
	loading: boolean;
	/** The note currently open in the centre, marked in the tree. */
	openPath: string | null;
	onOpenNote: (path: string) => void;
	onRefresh: () => void;
	/** The Search tab's term. Empty means the Notes tab is showing. */
	query: string;
	onQueryChange: (query: string) => void;
}) {
	const [tab, setTab] = useState<"notes" | "search">("notes");
	// Which folders the reader has opened or shut, read once on mount and
	// written through on every toggle. Only toggled folders live here; the rest
	// follow `defaultOpen`, which is what keeps a 55-folder vault from writing
	// 55 keys the first time anyone scrolls it.
	const [folders, setFolders] = useState<WikiFolderState>(loadFolderState);
	const toggleFolder = useCallback((path: string, open: boolean) => {
		setFolders((current) => ({ ...current, [path]: open }));
		saveFolderState(path, open);
	}, []);
	const notes = useMemo(() => files?.notes ?? [], [files]);
	const tree = useMemo(() => buildTree(notes), [notes]);
	const counts = useMemo(() => summarise(notes), [notes]);

	const matches = useMemo(() => {
		const needle = query.trim().toLowerCase();
		if (needle === "") return [];
		return notes.filter((note) => note.path.toLowerCase().includes(needle)).slice(0, 200);
	}, [notes, query]);

	return (
		<div className="wiki-rail">
			<div className="session-inspector__tabs">
				<button
					type="button"
					className={`session-inspector__tab${tab === "notes" ? " is-active" : ""}`}
					onClick={() => setTab("notes")}
				>
					<span className="session-inspector__tab-icon">
						<BookText aria-hidden="true" />
					</span>
					<span className="session-inspector__tab-label">Notes</span>
				</button>
				<button
					type="button"
					className={`session-inspector__tab${tab === "search" ? " is-active" : ""}`}
					onClick={() => setTab("search")}
				>
					<span className="session-inspector__tab-icon">
						<Search aria-hidden="true" />
					</span>
					<span className="session-inspector__tab-label">Search</span>
				</button>
			</div>

			{tab === "notes" ? (
				<>
					<div className="wiki-rail__summary">
						<span className="wiki-rail__count">
							{loading && notes.length === 0
								? "Reading the vault…"
								: `${counts.notes} note${counts.notes === 1 ? "" : "s"} · ${counts.folders} folder${
										counts.folders === 1 ? "" : "s"
									}`}
						</span>
						<button type="button" className="wiki-rail__refresh" aria-label="Re-read the vault" onClick={onRefresh}>
							<RefreshCw aria-hidden="true" />
						</button>
					</div>
					<div className="wiki-rail__tree">
						{tree.map((node) => (
							<TreeRow
								key={node.path}
								node={node}
								depth={0}
								openPath={openPath}
								onOpenNote={onOpenNote}
								folders={folders}
								onToggleFolder={toggleFolder}
							/>
						))}
						{!loading && notes.length === 0 && <div className="wiki-rail__empty">This vault has no files yet.</div>}
						{files?.truncated && (
							<div className="wiki-rail__empty">
								Only the first {notes.length} files are listed — this folder is larger than a note vault.
							</div>
						)}
					</div>
				</>
			) : (
				<>
					<div className="wiki-rail__search">
						<Search aria-hidden="true" className="wiki-rail__search-icon" />
						<input
							className="wiki-rail__search-input"
							placeholder="Find a note by name"
							spellCheck={false}
							value={query}
							onChange={(event) => onQueryChange(event.target.value)}
						/>
					</div>
					<div className="wiki-rail__tree">
						{query.trim() === "" && <div className="wiki-rail__empty">Type to find a note by its name or folder.</div>}
						{query.trim() !== "" && matches.length === 0 && (
							<div className="wiki-rail__empty">Nothing in the vault matches that.</div>
						)}
						{matches.map((note) => (
							<button
								type="button"
								key={note.path}
								className={`wiki-rail__row wiki-rail__row--file${
									note.path === openPath ? " wiki-rail__row--open" : ""
								}`}
								style={{ paddingLeft: 8 }}
								onClick={() => onOpenNote(note.path)}
							>
								<span className="wiki-rail__name">{note.path}</span>
								<span className="wiki-rail__age">{compactAge(note.modifiedAt)}</span>
							</button>
						))}
					</div>
				</>
			)}
		</div>
	);
}

function TreeRow({
	node,
	depth,
	openPath,
	onOpenNote,
	folders,
	onToggleFolder,
}: {
	node: TreeNode;
	depth: number;
	openPath: string | null;
	onOpenNote: (path: string) => void;
	folders: WikiFolderState;
	onToggleFolder: (path: string, open: boolean) => void;
}) {
	// A folder the reader has opened or shut before is drawn the way they left
	// it; one they have never touched follows the tree's own default. The state
	// is held by the RAIL rather than by the row so it survives the row
	// unmounting — which is what expanding a parent does to every child.
	const open = folders[node.path] ?? defaultOpen(node.path, depth, openPath);

	if (node.kind === "file") {
		const isOpen = node.path === openPath;
		return (
			<button
				type="button"
				className={`wiki-rail__row wiki-rail__row--file${isOpen ? " wiki-rail__row--open" : ""}`}
				style={{ paddingLeft: 8 + depth * 17 + 17 }}
				onClick={() => onOpenNote(node.path)}
			>
				<span className="wiki-rail__name">{node.name}</span>
				<span className="wiki-rail__age">{compactAge(node.modifiedAt)}</span>
			</button>
		);
	}

	return (
		<>
			<button
				type="button"
				className="wiki-rail__row wiki-rail__row--folder"
				style={{ paddingLeft: 8 + depth * 17 }}
				aria-expanded={open}
				onClick={() => onToggleFolder(node.path, !open)}
			>
				{open ? (
					<ChevronDown aria-hidden="true" className="wiki-rail__chevron" />
				) : (
					<ChevronRight aria-hidden="true" className="wiki-rail__chevron" />
				)}
				<span className="wiki-rail__name wiki-rail__name--folder">{node.name}</span>
				<span className="wiki-rail__age">{node.noteCount}</span>
			</button>
			{open &&
				node.children.map((child) => (
					<TreeRow
						key={child.path}
						node={child}
						depth={depth + 1}
						openPath={openPath}
						onOpenNote={onOpenNote}
						folders={folders}
						onToggleFolder={onToggleFolder}
					/>
				))}
		</>
	);
}

/** Folders before files, each alphabetical — the order a file browser uses. */
export function buildTree(notes: { path: string; modifiedAt?: string }[]): TreeNode[] {
	const root: TreeFolder = { kind: "folder", name: "", path: "", children: [], noteCount: 0 };
	const folders = new Map<string, TreeFolder>([["", root]]);

	for (const note of notes) {
		const segments = note.path.split("/");
		const fileName = segments.pop();
		if (!fileName) continue;
		let parent = root;
		let prefix = "";
		for (const segment of segments) {
			prefix = prefix === "" ? segment : `${prefix}/${segment}`;
			let folder = folders.get(prefix);
			if (!folder) {
				folder = { kind: "folder", name: segment, path: prefix, children: [], noteCount: 0 };
				folders.set(prefix, folder);
				parent.children.push(folder);
			}
			parent = folder;
		}
		parent.children.push({ kind: "file", name: fileName, path: note.path, modifiedAt: note.modifiedAt });
		// Every ancestor counts this file, which is what a folder's number means.
		let counted = "";
		for (const segment of segments) {
			counted = counted === "" ? segment : `${counted}/${segment}`;
			const folder = folders.get(counted);
			if (folder) folder.noteCount += 1;
		}
	}

	const sortNodes = (nodes: TreeNode[]) => {
		nodes.sort((a, b) => {
			if (a.kind !== b.kind) return a.kind === "folder" ? -1 : 1;
			return a.name.localeCompare(b.name);
		});
		for (const node of nodes) if (node.kind === "folder") sortNodes(node.children);
	};
	sortNodes(root.children);
	return root.children;
}

/** "148 notes · 12 folders" — notes are the markdown, folders are every level. */
export function summarise(notes: { path: string }[]): { notes: number; folders: number } {
	const folders = new Set<string>();
	let count = 0;
	for (const note of notes) {
		if (note.path.toLowerCase().endsWith(".md")) count += 1;
		const segments = note.path.split("/");
		segments.pop();
		let prefix = "";
		for (const segment of segments) {
			prefix = prefix === "" ? segment : `${prefix}/${segment}`;
			folders.add(prefix);
		}
	}
	return { notes: count, folders: folders.size };
}

/**
 * A rail-width age: "2d", "3w", "1y". Deliberately terser than the app's
 * `relativeTime` — this column is 30px wide next to a filename, and "2d ago"
 * spends a third of it on a word that every row repeats.
 */
export function compactAge(iso: string | undefined, now = Date.now()): string {
	if (!iso) return "";
	const at = Date.parse(iso);
	if (Number.isNaN(at)) return "";
	const minutes = Math.max(0, Math.floor((now - at) / 60_000));
	if (minutes < 60) return `${minutes}m`;
	const hours = Math.floor(minutes / 60);
	if (hours < 24) return `${hours}h`;
	const days = Math.floor(hours / 24);
	if (days < 7) return `${days}d`;
	const weeks = Math.floor(days / 7);
	if (weeks < 52) return `${weeks}w`;
	return `${Math.floor(days / 365)}y`;
}
