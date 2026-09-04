/**
 * Which folders of the vault rail the reader has opened or shut, remembered
 * across leaving the Wiki page and coming back.
 *
 * Stored in `localStorage` under the same `ao.<area>.<thing>` + guarded-access
 * convention `files-panel-state.ts` and `ao.projects.collapsed` use. This is
 * per-VIEWER interface state, not vault state: the daemon has no business
 * knowing which folders someone likes open, and a second machine reading the
 * same vault should get its own arrangement.
 *
 * 🗝 The store holds the reader's DEVIATION from the tree's own defaults, not a
 * full picture. `defaultOpen` below opens the top level and the folder holding
 * the note being read, and shuts everything deeper; only a folder the reader
 * actually clicked gets a row here. A 55-folder vault therefore writes a
 * handful of keys rather than 55, and a folder whose default later changes
 * still follows the default until someone touches it.
 */

const STATE_KEY = "ao.wiki.collapsed";

/**
 * How many folders are remembered. A cap rather than a policy question: a vault
 * that gets reorganised would otherwise grow this blob forever with paths that
 * no longer exist. The least-recently-touched folder is the one dropped.
 */
const MAX_FOLDERS = 400;

/** path → open?, for the folders the reader has toggled. */
export type WikiFolderState = Record<string, boolean>;

type StoredEntry = { open: boolean; at: number };
type Stored = Record<string, StoredEntry>;

function storage(): Storage | null {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

function readStored(): Stored {
	try {
		const raw = storage()?.getItem(STATE_KEY);
		if (!raw) return {};
		const parsed: unknown = JSON.parse(raw);
		if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return {};
		const out: Stored = {};
		for (const [path, value] of Object.entries(parsed as Record<string, unknown>)) {
			if (typeof value !== "object" || value === null) continue;
			const entry = value as { open?: unknown; at?: unknown };
			if (typeof entry.open !== "boolean") continue;
			out[path] = { open: entry.open, at: typeof entry.at === "number" ? entry.at : 0 };
		}
		return out;
	} catch {
		// Private-mode / disabled / corrupt storage must not take the rail down
		// with it: an unreadable arrangement is the same as no arrangement.
		return {};
	}
}

/** The folders the reader has toggled, as `path → open?`. */
export function loadFolderState(): WikiFolderState {
	const stored = readStored();
	const state: WikiFolderState = {};
	for (const [path, entry] of Object.entries(stored)) state[path] = entry.open;
	return state;
}

/** Records one folder's new state, evicting the oldest once past the cap. */
export function saveFolderState(path: string, open: boolean, now = Date.now()): void {
	const store = storage();
	if (!store || path === "") return;
	const stored = readStored();
	stored[path] = { open, at: now };
	const paths = Object.keys(stored);
	if (paths.length > MAX_FOLDERS) {
		paths.sort((a, b) => stored[a].at - stored[b].at);
		for (const stale of paths.slice(0, paths.length - MAX_FOLDERS)) delete stored[stale];
	}
	try {
		store.setItem(STATE_KEY, JSON.stringify(stored));
	} catch {
		// A full or disabled quota loses the arrangement, not the rail.
	}
}

/**
 * Whether a folder the reader has never touched starts open.
 *
 * Top-level folders start OPEN: a rail of folder names says nothing about what
 * is in the vault, and the point of the rail is to reach a note. Deeper levels
 * start closed so a nested vault does not unroll into a wall — except the one
 * holding the note being read, which reveals where that note sits.
 */
export function defaultOpen(path: string, depth: number, openPath: string | null): boolean {
	return depth === 0 || (openPath !== null && openPath.startsWith(`${path}/`));
}

/**
 * Which way round the tree lists its entries: `asc` is the tree's own order
 * (folders alphabetically, then the notes with the newest edit on top), `desc`
 * that same order inverted within each group.
 *
 * A sibling of the folder state above, and per-VIEWER for the same reason: the
 * direction someone likes reading their vault in is not a fact about the vault.
 * Kept in its own key rather than folded into the folder blob so the eviction
 * cap there can never drop it.
 */
const SORT_KEY = "ao.wiki.sort";

export type WikiSortOrder = "asc" | "desc";

/** The direction the reader last chose, defaulting to the tree's own order. */
export function loadSortOrder(): WikiSortOrder {
	try {
		return storage()?.getItem(SORT_KEY) === "desc" ? "desc" : "asc";
	} catch {
		return "asc";
	}
}

/** Records the direction the reader just chose. */
export function saveSortOrder(order: WikiSortOrder): void {
	try {
		storage()?.setItem(SORT_KEY, order);
	} catch {
		// A full or disabled quota loses the direction, not the rail.
	}
}

/* -------------------------------------------------------------------------
 * The Tasks tab's per-viewer state
 *
 * The tab's real CONFIGURATION — which subtree, which sections, the cutoff,
 * the owner aliases — lives in the daemon, because it decides what is scanned
 * and it is a fact about the vault. What lives here is only how one person
 * likes to look at it, which is nobody else's business and no reason to write
 * to disk on another machine.
 * ---------------------------------------------------------------------- */

const OWNER_FILTER_KEY = "ao.wiki.tasks.owner";
const SHOW_HIDDEN_KEY = "ao.wiki.tasks.showHidden";
const COLLAPSED_GROUPS_KEY = "ao.wiki.tasks.collapsed";

/** Which rows the reader last asked for. */
export type WikiOwnerFilter = "all" | "mine" | "others";

export function loadOwnerFilter(): WikiOwnerFilter {
	try {
		const raw = storage()?.getItem(OWNER_FILTER_KEY);
		return raw === "mine" || raw === "others" ? raw : "all";
	} catch {
		return "all";
	}
}

export function saveOwnerFilter(filter: WikiOwnerFilter): void {
	try {
		storage()?.setItem(OWNER_FILTER_KEY, filter);
	} catch {
		// A full or disabled quota loses the preference, not the tab.
	}
}

/**
 * Whether the rows the cutoff hides are being shown.
 *
 * It defaults to FALSE — hiding is what the cutoff is for — but it is
 * remembered, because someone who turned it on is working through the backlog
 * and having it snap shut every visit would be its own small hostility.
 */
export function loadShowHidden(): boolean {
	try {
		return storage()?.getItem(SHOW_HIDDEN_KEY) === "1";
	} catch {
		return false;
	}
}

export function saveShowHidden(show: boolean): void {
	try {
		storage()?.setItem(SHOW_HIDDEN_KEY, show ? "1" : "0");
	} catch {
		// As above.
	}
}

/** The day groups the reader has collapsed, as a set of group keys. */
export function loadCollapsedGroups(): Record<string, boolean> {
	try {
		const raw = storage()?.getItem(COLLAPSED_GROUPS_KEY);
		if (!raw) return {};
		const parsed: unknown = JSON.parse(raw);
		if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return {};
		const out: Record<string, boolean> = {};
		for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
			if (typeof value === "boolean") out[key] = value;
		}
		return out;
	} catch {
		return {};
	}
}

export function saveCollapsedGroups(state: Record<string, boolean>): void {
	try {
		// Only the collapsed ones are stored. A dated group's key is a DATE, so
		// keeping the expanded ones would grow this blob by one key a day
		// forever, for days that have long since gone.
		const collapsed: Record<string, boolean> = {};
		for (const [key, value] of Object.entries(state)) if (value) collapsed[key] = true;
		storage()?.setItem(COLLAPSED_GROUPS_KEY, JSON.stringify(collapsed));
	} catch {
		// As above.
	}
}
