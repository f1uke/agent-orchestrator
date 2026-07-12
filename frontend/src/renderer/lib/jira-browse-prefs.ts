// Browse Jira remembers how you left the view — whether issues are grouped by
// sprint and which assignee is filtered — alongside the last-project memory, using
// the same ao.<area>.<thing> key + guarded-access convention as jira-last-project.
const browsePrefsKey = "ao.jira.browsePrefs";

export type BrowsePrefs = {
	/** Group the issue list into sprint sections (default on — matches the board). */
	groupBySprint: boolean;
	/** Selected assignee filter: "" = all, the UNASSIGNED sentinel, or a name. */
	assignee: string;
};

const defaultPrefs: BrowsePrefs = { groupBySprint: true, assignee: "" };

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
