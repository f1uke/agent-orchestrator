import type { JiraProject } from "../hooks/useSessionJiraContext";

// Browse Jira lets you star projects in the picker; starred projects pin to the
// top so a return trip surfaces your favorites first. Stored globally (independent
// of the AO workspace) alongside the last-pick memory, using the same
// ao.<area>.<thing> key + guarded-access convention as jira-last-project.
//
// We persist the full {key, name} — not just the key — so the "Starred" group can
// render a favorite even when it is NOT in the currently-fetched project page
// (the project list is capped at 100 by key order, so a starred project whose key
// sorts past the cap would otherwise vanish from the group).
const starredStorageKey = "ao.jira.starredProjects";

function getLocalStorage(): Storage | null {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

/** Reads the starred projects (key + name), tolerating the legacy `string[]`
 *  (keys-only) form. Empty array when none/invalid. */
export function readStarredProjects(): JiraProject[] {
	const raw = getLocalStorage()?.getItem(starredStorageKey);
	if (!raw) return [];
	try {
		const parsed = JSON.parse(raw);
		if (Array.isArray(parsed)) {
			return parsed
				.map((entry): JiraProject | null => {
					// Legacy form: a bare key string.
					if (typeof entry === "string" && entry.length > 0) return { key: entry };
					// Current form: { key, name? }.
					if (entry && typeof entry.key === "string" && entry.key.length > 0) {
						return { key: entry.key, name: typeof entry.name === "string" ? entry.name : undefined };
					}
					return null;
				})
				.filter((project): project is JiraProject => project !== null);
		}
	} catch {
		// Corrupt value — treat as no favorites.
	}
	return [];
}

/** Persists the starred projects (best-effort; ignores quota/serialization errors). */
export function writeStarredProjects(projects: JiraProject[]): void {
	const store = getLocalStorage();
	if (!store) return;
	try {
		store.setItem(
			starredStorageKey,
			JSON.stringify(projects.map((project) => ({ key: project.key, name: project.name }))),
		);
	} catch {
		// Storage full or unavailable — favorites are a nicety, never fatal.
	}
}
