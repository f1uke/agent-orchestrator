/**
 * Path-list → folder-tree model for the Files panel.
 *
 * Deliberately generic over the item type and free of any Changes-mode
 * vocabulary: Browse mode (the full worktree tree, shipping separately) is meant
 * to consume this module and `FileTree.tsx` as-is rather than grow a second tree
 * implementation.
 */

export type FileTreeNode<T> = FileTreeDir<T> | FileTreeFile<T>;

export type FileTreeDir<T> = {
	kind: "dir";
	/** Full path of the directory — stable identity for the collapsed set. */
	key: string;
	/** What the row shows; a collapsed chain reads `internal/service/session`. */
	label: string;
	children: FileTreeNode<T>[];
};

export type FileTreeFile<T> = {
	kind: "file";
	/** The file's full path. */
	key: string;
	label: string;
	item: T;
};

export type FileTreeRow<T> = {
	node: FileTreeNode<T>;
	depth: number;
	/** Directories only: whether this row's children are currently rendered. */
	expanded?: boolean;
};

type Draft<T> = {
	dirs: Map<string, Draft<T>>;
	files: { name: string; path: string; item: T }[];
};

const newDraft = <T>(): Draft<T> => ({ dirs: new Map(), files: [] });

/**
 * Build a folder tree from a flat list of paths.
 *
 * A directory whose only child is another DIRECTORY merges into it, so
 * `backend/internal/service/session` is one row and one indent instead of four
 * of each — the mechanism that keeps this repo's four-plus-level paths readable
 * in a 280px rail. Merging stops at a branch point, and a directory holding a
 * single FILE keeps its own row: this mirrors GitLab's merge-request tree, where
 * `Commons/` and the one file inside it are two rows.
 */
export function buildFileTree<T>(items: readonly T[], getPath: (item: T) => string): FileTreeNode<T>[] {
	const root = newDraft<T>();
	for (const item of items) {
		const path = getPath(item);
		const segments = path.split("/").filter(Boolean);
		if (segments.length === 0) continue;
		const name = segments[segments.length - 1];
		let node = root;
		for (const segment of segments.slice(0, -1)) {
			let next = node.dirs.get(segment);
			if (!next) {
				next = newDraft<T>();
				node.dirs.set(segment, next);
			}
			node = next;
		}
		node.files.push({ name, path, item });
	}
	return materialize(root, "");
}

function materialize<T>(draft: Draft<T>, prefix: string): FileTreeNode<T>[] {
	const dirs: FileTreeDir<T>[] = [];
	const files: FileTreeFile<T>[] = draft.files.map((f) => ({
		kind: "file",
		key: f.path,
		label: f.name,
		item: f.item,
	}));
	for (const [name, child] of draft.dirs) {
		const key = prefix ? `${prefix}/${name}` : name;
		dirs.push(collapseChain({ kind: "dir", key, label: name, children: materialize(child, key) }));
	}
	dirs.sort(byLabel);
	files.sort(byLabel);
	// Directories first, then files — the ordering every file tree uses, and the
	// one that keeps a folder's contents visually attached to it.
	return [...dirs, ...files];
}

/** Merge a chain of only-child DIRECTORIES into one row, GitLab-style. */
function collapseChain<T>(dir: FileTreeDir<T>): FileTreeDir<T> {
	let node = dir;
	while (node.children.length === 1 && node.children[0].kind === "dir") {
		const only = node.children[0];
		node = { kind: "dir", key: only.key, label: `${node.label}/${only.label}`, children: only.children };
	}
	return node;
}

function byLabel<T>(a: FileTreeNode<T>, b: FileTreeNode<T>): number {
	return a.label.localeCompare(b.label, undefined, { sensitivity: "base" });
}

/**
 * Depth-first row list for rendering, skipping the children of any directory
 * whose key is in `collapsed`.
 *
 * Directories are expanded by DEFAULT (the set names the closed ones): a changed
 * -files tree that opened fully collapsed would hide every file the reviewer
 * came for.
 */
export function flattenFileTree<T>(
	nodes: readonly FileTreeNode<T>[],
	collapsed: ReadonlySet<string>,
	depth = 0,
): FileTreeRow<T>[] {
	const rows: FileTreeRow<T>[] = [];
	for (const node of nodes) {
		if (node.kind === "file") {
			rows.push({ node, depth });
			continue;
		}
		const expanded = !collapsed.has(node.key);
		rows.push({ node, depth, expanded });
		if (expanded) rows.push(...flattenFileTree(node.children, collapsed, depth + 1));
	}
	return rows;
}

/**
 * Does `path` match the panel's search box?
 *
 * Substring by default — searching `fix` has to find `hotfix/login-crash`, and a
 * prefix-only filter is the bug this panel shipped once already. A query
 * carrying `*` or `?` is instead read as a whole-path glob, which is what
 * GitLab's own `e.g. *.vue` hint promises.
 */
export function matchesFileQuery(path: string, query: string): boolean {
	const trimmed = query.trim();
	if (!trimmed) return true;
	if (/[*?]/.test(trimmed)) return globToRegExp(trimmed).test(path);
	return path.toLowerCase().includes(trimmed.toLowerCase());
}

function globToRegExp(glob: string): RegExp {
	// Escape everything regex-special, then re-open only the glob wildcards, so a
	// literal dot stays a dot.
	const source = glob
		.replace(/[.+^${}()|[\]\\]/g, "\\$&")
		.replace(/\*/g, "[^]*")
		.replace(/\?/g, "[^]");
	return new RegExp(`^${source}$`, "i");
}
