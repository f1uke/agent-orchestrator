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
 */
export function FileTree<T>({
	nodes,
	collapsed,
	onToggleDir,
	onSelectFile,
	selectedKey,
	revealedKey,
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
				const indent = indentFor(depth);
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
						className={cn(
							"file-tree__row file-tree__row--file",
							selected && "is-selected",
							revealedKey != null && key === revealedKey && "is-revealed",
						)}
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
