/**
 * How each TASK had its Files rail arranged, remembered across switching away
 * and back.
 *
 * Keyed by `taskKeyOf` — see that function for why a task and not a session.
 * Stored in `localStorage` under the same `ao.<area>.<thing>` + guarded-access
 * convention `jira-browse-prefs.ts` uses. Electron's `userData` is pinned to
 * `~/.ao/electron` (`frontend/src/main.ts`), so renderer storage already
 * satisfies the "everything under ~/.ao" hard rule; nothing here goes near an
 * OS-default app-data location.
 *
 * 🗝 Each mode stores its DEVIATION from its own default, never the whole
 * picture:
 *
 * - Browse opens with every folder shut, so what is worth keeping is the handful
 *   the reader OPENED;
 * - Changes opens fully expanded (it is a diff — every row is a row the reviewer
 *   came for), so what is worth keeping is the handful they CLOSED.
 *
 * Either way the stored set is tens of keys rather than the thousands a
 * 7,000-file worktree would otherwise write on every toggle.
 *
 * The search box is deliberately NOT remembered. Coming back to a rail that
 * silently hides every file behind a query you do not remember typing is worse
 * than coming back to a full tree.
 */

export type FilesMode = "changes" | "browse";
export type FilesView = "tree" | "list";

export type FilesPanelState = {
	mode: FilesMode;
	view: FilesView;
	/** Browse's deviation: directory keys the reader opened. */
	browseExpanded: string[];
	/** Changes' deviation: directory keys the reader closed. */
	changesCollapsed: string[];
	/** Scroll offset per mode — one offset restored into the other mode's tree lands nowhere. */
	browseScroll: number;
	changesScroll: number;
};

/** The global fallbacks, from before any of this was per task. */
const GLOBAL_VIEW_KEY = "ao.files.view";
const GLOBAL_MODE_KEY = "ao.files.mode";
const STATE_KEY = "ao.files.state";

/**
 * How many tasks are remembered, and how many keys each fold set may hold.
 *
 * A cap rather than a policy question: an app that ran for a year would
 * otherwise grow this blob forever, and nobody would ever see it happen. The
 * least-recently-touched task is the one dropped.
 */
const MAX_TASKS = 40;
const MAX_KEYS = 500;

type StoredEntry = FilesPanelState & { at: number };

function storage(): Storage | null {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

/**
 * The mode a task that has never been arranged opens in.
 *
 * Changes stays the default: a worker's rail is opened to see what the agent did
 * far more often than to go looking for a file by name. The GLOBAL value is
 * consulted first, so a new worker inherits the reader's habit and an arranged
 * one keeps its own arrangement.
 */
export function globalMode(): FilesMode {
	try {
		return storage()?.getItem(GLOBAL_MODE_KEY) === "browse" ? "browse" : "changes";
	} catch {
		// Private-mode / disabled storage must not take the panel down with it.
		return "changes";
	}
}

export function globalView(): FilesView {
	try {
		return storage()?.getItem(GLOBAL_VIEW_KEY) === "list" ? "list" : "tree";
	} catch {
		return "tree";
	}
}

/** Records the reader's latest mode/view as the habit a NEW task inherits. */
export function writeGlobalMode(mode: FilesMode): void {
	try {
		storage()?.setItem(GLOBAL_MODE_KEY, mode);
	} catch {
		// Remembering is a nicety, never fatal.
	}
}

export function writeGlobalView(view: FilesView): void {
	try {
		storage()?.setItem(GLOBAL_VIEW_KEY, view);
	} catch {
		// Remembering is a nicety, never fatal.
	}
}

export function defaultState(): FilesPanelState {
	return {
		mode: globalMode(),
		view: globalView(),
		browseExpanded: [],
		changesCollapsed: [],
		browseScroll: 0,
		changesScroll: 0,
	};
}

function readAll(): Record<string, StoredEntry> {
	let raw: string | null = null;
	try {
		raw = storage()?.getItem(STATE_KEY) ?? null;
	} catch {
		return {};
	}
	if (!raw) return {};
	try {
		const parsed: unknown = JSON.parse(raw);
		if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
		return parsed as Record<string, StoredEntry>;
	} catch {
		// Corrupt value — start over rather than take the rail down.
		return {};
	}
}

const strings = (value: unknown): string[] =>
	Array.isArray(value) ? value.filter((v): v is string => typeof v === "string").slice(0, MAX_KEYS) : [];

const offset = (value: unknown): number =>
	typeof value === "number" && Number.isFinite(value) && value > 0 ? value : 0;

/**
 * How the given task had its rail arranged, falling back field by field.
 *
 * Field by field rather than all-or-nothing on purpose: a stored entry written
 * by an older build is missing whatever that build did not have, and losing the
 * folds because the scroll offset is absent would be a silly way to forget.
 */
export function readFilesPanelState(taskKey: string): FilesPanelState {
	const stored = readAll()[taskKey] as Partial<StoredEntry> | undefined;
	const fallback = defaultState();
	if (!stored || typeof stored !== "object") return fallback;
	return {
		mode: stored.mode === "browse" || stored.mode === "changes" ? stored.mode : fallback.mode,
		view: stored.view === "list" || stored.view === "tree" ? stored.view : fallback.view,
		browseExpanded: strings(stored.browseExpanded),
		changesCollapsed: strings(stored.changesCollapsed),
		browseScroll: offset(stored.browseScroll),
		changesScroll: offset(stored.changesScroll),
	};
}

/**
 * Persists one task's arrangement, evicting the least recently touched once
 * `MAX_TASKS` is exceeded. Best-effort: a full or unavailable store means the
 * rail forgets, never that it breaks.
 */
export function writeFilesPanelState(taskKey: string, state: FilesPanelState, now = Date.now()): void {
	const store = storage();
	if (!store) return;
	const all = readAll();
	all[taskKey] = {
		mode: state.mode,
		view: state.view,
		// Newest keys win the cap: they are the folders the reader touched last.
		browseExpanded: state.browseExpanded.slice(-MAX_KEYS),
		changesCollapsed: state.changesCollapsed.slice(-MAX_KEYS),
		browseScroll: Math.max(0, Math.round(state.browseScroll)),
		changesScroll: Math.max(0, Math.round(state.changesScroll)),
		at: now,
	};
	const keys = Object.keys(all);
	if (keys.length > MAX_TASKS) {
		keys
			.sort((a, b) => (all[a]?.at ?? 0) - (all[b]?.at ?? 0))
			.slice(0, keys.length - MAX_TASKS)
			.forEach((key) => delete all[key]);
	}
	try {
		store.setItem(STATE_KEY, JSON.stringify(all));
	} catch {
		// Storage full or unavailable — remembering is a nicety, never fatal.
	}
}
