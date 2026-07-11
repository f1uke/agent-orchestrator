import type { JiraProject } from "../hooks/useSessionJiraContext";

// Browse Jira remembers the last project you picked so a return trip lands on the
// same project without re-searching. Stored globally (independent of the AO
// workspace) to match the design ("remembers your last pick"). Same
// ao.<area>.<thing> key + guarded-access convention as ui-store.
const lastProjectStorageKey = "ao.jira.lastProject";

function getLocalStorage(): Storage | null {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

/** Reads the remembered Jira project, or null when none/invalid. */
export function readLastJiraProject(): JiraProject | null {
	const raw = getLocalStorage()?.getItem(lastProjectStorageKey);
	if (!raw) return null;
	try {
		const parsed = JSON.parse(raw);
		if (parsed && typeof parsed.key === "string" && parsed.key) {
			return { key: parsed.key, name: typeof parsed.name === "string" ? parsed.name : undefined };
		}
	} catch {
		// Corrupt value — treat as no remembered project.
	}
	return null;
}

/** Persists the picked Jira project (best-effort; ignores quota/serialization errors). */
export function writeLastJiraProject(project: JiraProject): void {
	const store = getLocalStorage();
	if (!store || !project.key) return;
	try {
		store.setItem(lastProjectStorageKey, JSON.stringify({ key: project.key, name: project.name }));
	} catch {
		// Storage full or unavailable — remembering is a nicety, never fatal.
	}
}
