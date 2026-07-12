// Browse Jira remembers how you left the view — whether issues are grouped by
// sprint and which assignee is filtered — alongside the last-project memory, using
// the same ao.<area>.<thing> key + guarded-access convention as jira-last-project.
const browsePrefsKey = "ao.jira.browsePrefs";

export type BrowsePrefs = {
	/** Group the issue list into sprint sections (default on — matches the board). */
	groupBySprint: boolean;
	/** Selected assignee filter: "" = all, the UNASSIGNED sentinel, or a name. */
	assignee: string;
	/** Exclude done issues (statusCategory != Done). Default off. */
	hideDone: boolean;
	/** Only issues in an open sprint (sprint in openSprints()). Default off. */
	activeSprintOnly: boolean;
	/** Advanced (raw-JQL) mode: the JQL box drives the search and the structured
	 *  filters are hidden. Default off. */
	advancedMode: boolean;
	/** The raw JQL typed in advanced mode (remembered across visits). */
	advancedJql: string;
};

const defaultPrefs: BrowsePrefs = {
	groupBySprint: true,
	assignee: "",
	hideDone: false,
	activeSprintOnly: false,
	advancedMode: false,
	advancedJql: "",
};

function getLocalStorage(): Storage | null {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

/** Reads the remembered Browse Jira view prefs (defaults when none/invalid). */
export function readBrowsePrefs(): BrowsePrefs {
	const raw = getLocalStorage()?.getItem(browsePrefsKey);
	if (!raw) return { ...defaultPrefs };
	try {
		const parsed = JSON.parse(raw);
		return {
			groupBySprint: typeof parsed.groupBySprint === "boolean" ? parsed.groupBySprint : defaultPrefs.groupBySprint,
			assignee: typeof parsed.assignee === "string" ? parsed.assignee : defaultPrefs.assignee,
			hideDone: typeof parsed.hideDone === "boolean" ? parsed.hideDone : defaultPrefs.hideDone,
			activeSprintOnly:
				typeof parsed.activeSprintOnly === "boolean" ? parsed.activeSprintOnly : defaultPrefs.activeSprintOnly,
			advancedMode: typeof parsed.advancedMode === "boolean" ? parsed.advancedMode : defaultPrefs.advancedMode,
			advancedJql: typeof parsed.advancedJql === "string" ? parsed.advancedJql : defaultPrefs.advancedJql,
		};
	} catch {
		// Corrupt value — fall back to defaults.
	}
	return { ...defaultPrefs };
}

/** Persists the Browse Jira view prefs (best-effort; ignores quota/serialization errors). */
export function writeBrowsePrefs(prefs: BrowsePrefs): void {
	const store = getLocalStorage();
	if (!store) return;
	try {
		store.setItem(browsePrefsKey, JSON.stringify(prefs));
	} catch {
		// Storage full or unavailable — remembering is a nicety, never fatal.
	}
}

// Per-node collapse state for the Browse Jira issue tree (Fix 2): the set of node
// keys the user has collapsed. Default is expanded (a key absent = expanded), so an
// empty set shows the whole tree. Kept separate from BrowsePrefs — it's a growing
// set of issue keys, not a fixed view config.
const collapsedNodesKey = "ao.jira.browseCollapsed";

/** Reads the set of collapsed tree-node keys (empty when none/invalid). */
export function readCollapsedNodes(): Set<string> {
	const raw = getLocalStorage()?.getItem(collapsedNodesKey);
	if (!raw) return new Set();
	try {
		const parsed = JSON.parse(raw);
		if (Array.isArray(parsed)) return new Set(parsed.filter((k): k is string => typeof k === "string"));
	} catch {
		// Corrupt value — start expanded.
	}
	return new Set();
}

/** Persists the collapsed tree-node keys (best-effort). */
export function writeCollapsedNodes(keys: ReadonlySet<string>): void {
	const store = getLocalStorage();
	if (!store) return;
	try {
		store.setItem(collapsedNodesKey, JSON.stringify([...keys]));
	} catch {
		// Storage full or unavailable — remembering is a nicety, never fatal.
	}
}
