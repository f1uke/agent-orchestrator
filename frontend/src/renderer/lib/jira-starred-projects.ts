// Browse Jira lets you star projects in the picker; starred projects pin to the
// top so a return trip surfaces your favorites first. Stored globally (independent
// of the AO workspace) alongside the last-pick memory, using the same
// ao.<area>.<thing> key + guarded-access convention as jira-last-project.
const starredStorageKey = "ao.jira.starredProjects";

function getLocalStorage(): Storage | null {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

/** Reads the set of starred Jira project keys (empty set when none/invalid). */
export function readStarredProjects(): Set<string> {
	const raw = getLocalStorage()?.getItem(starredStorageKey);
	if (!raw) return new Set();
	try {
		const parsed = JSON.parse(raw);
		if (Array.isArray(parsed)) {
			return new Set(parsed.filter((k): k is string => typeof k === "string" && k.length > 0));
		}
	} catch {
		// Corrupt value — treat as no favorites.
	}
	return new Set();
}

/** Persists the starred keys (best-effort; ignores quota/serialization errors). */
export function writeStarredProjects(keys: Set<string>): void {
	const store = getLocalStorage();
	if (!store) return;
	try {
		store.setItem(starredStorageKey, JSON.stringify([...keys]));
	} catch {
		// Storage full or unavailable — favorites are a nicety, never fatal.
	}
}
