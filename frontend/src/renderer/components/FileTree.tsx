import { File, FileCode2, FileCog, FileImage, FileJson2, FileText, FileType2, Folder, FolderOpen } from "lucide-react";
import type { ReactNode } from "react";
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

/** Indent per level, and the level at which indenting stops growing. */
const INDENT_STEP = 11;
const INDENT_BASE = 8;
const MAX_INDENT_DEPTH = 4;

/**
 * A collapsible folder tree over any item type.
 *
 * Generic and payload-agnostic by design: Changes mode supplies changed files
 * and a ±counts meta column, and Browse mode (the full worktree tree, shipping
 * separately) is meant to drop straight in with its own items and renderers
 * rather than grow a second tree.
 *
 * Indent is capped at four levels because the rail's content floor is 280px —
 * past that, indenting costs more than the hierarchy is worth. Single-child
 * directory chains are already merged upstream in `buildFileTree`, which is what
 * keeps this repo's four-plus-level paths to one or two levels here.
 */
export function FileTree<T>({
	nodes,
	collapsed,
	onToggleDir,
	onSelectFile,
	selectedKey,
	label,
	renderLead,
	renderMeta,
	getFileKey,
	getFileLabel,
	getTitle,
}: {
	nodes: readonly FileTreeNode<T>[];
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
	const rows = flattenFileTree(nodes, collapsed);

	return (
		<div className="file-tree" role="tree" aria-label={label}>
			{rows.map(({ node, depth, expanded }) => {
				const indent = INDENT_BASE + Math.min(depth, MAX_INDENT_DEPTH) * INDENT_STEP;
				const guides = <IndentGuides depth={depth} />;
				if (node.kind === "dir") {
					return (
						<button
							key={node.key}
							type="button"
							role="treeitem"
							aria-expanded={expanded}
							aria-level={depth + 1}
							className="file-tree__row file-tree__row--dir"
							style={{ paddingLeft: indent }}
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
						aria-current={selected ? "true" : undefined}
						data-path={key}
						className={cn("file-tree__row file-tree__row--file", selected && "is-selected")}
						style={{ paddingLeft: indent }}
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
	);
}

/**
 * The hairlines that trace each open ancestor down the rows beneath it, as
 * GitLab's tree draws them. Purely decorative — depth is already exposed to
 * assistive tech via `aria-level`.
 */
function IndentGuides({ depth }: { depth: number }) {
	if (depth === 0) return null;
	return (
		<>
			{Array.from({ length: Math.min(depth, MAX_INDENT_DEPTH) }, (_, i) => (
				<span
					key={i}
					aria-hidden="true"
					className="file-tree__guide"
					style={{ left: INDENT_BASE + i * INDENT_STEP + 5 }}
				/>
			))}
		</>
	);
}
