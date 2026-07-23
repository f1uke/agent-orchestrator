import { useSyncExternalStore } from "react";
import type { AxisId } from "./cast";
import {
	chooseLook,
	chooseSpecies,
	clearLookChoice,
	clearSpeciesChoice,
	LOOKS_STORAGE_KEY,
	parseStoredLooks,
	pruneLookOverrides,
	pruneProjectLooks,
	serializeStoredLooks,
	type LookOverrides,
	type ProjectLooks,
	type StoredLooks,
} from "./look-store";
import type { SpeciesId } from "./species";

// The chosen looks, bound to localStorage and shared by BOTH windows.
//
// The app (`index.html`) and the overlay (`companion.html`) are two pages of the
// same origin: `app://renderer`, a scheme registered `standard` and `secure`, so it
// has a real origin rather than file://'s opaque null. That means one localStorage
// between them, and a `storage` event whenever the other window writes. Picking a
// colour in Settings therefore reaches the desktop with no IPC, no daemon round
// trip and nothing to keep in sync - there is one value and both windows read it.
//
// localStorage is the single source of truth, and `refreshLookOverrides` exists so
// the main process can NUDGE a re-read without carrying any data. Two channels that
// both mean "go and look again" cannot disagree; the slower one simply finds the
// work already done.
//
// The snapshot is cached because `useSyncExternalStore` compares by identity: a
// fresh object per read renders for ever.

let cache: StoredLooks | null = null;
const listeners = new Set<() => void>();

function storage(): Storage | null {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

function announce() {
	cache = null;
	for (const listener of listeners) listener();
}

/** Drop the cached snapshot and tell everyone. The IPC nudge lands here. */
export function refreshLookOverrides(): void {
	announce();
}

// A `storage` event fires in every OTHER document on the origin, never the one that
// wrote. `key === null` is what a whole-storage clear reports, and it has to count.
if (typeof window !== "undefined") {
	window.addEventListener("storage", (event) => {
		if (event.key !== null && event.key !== LOOKS_STORAGE_KEY) return;
		announce();
	});
}

/** Everything stored. Stable by identity until something actually changes. */
export function readStoredLooks(): StoredLooks {
	if (cache === null) cache = parseStoredLooks(storage()?.getItem(LOOKS_STORAGE_KEY) ?? null);
	return cache;
}

/** Every chosen look, per session. */
export function readLookOverrides(): LookOverrides {
	return readStoredLooks().sessions;
}

/** Every project's chosen creature. */
export function readProjectLooks(): ProjectLooks {
	return readStoredLooks().projects;
}

export function subscribeLookOverrides(listener: () => void): () => void {
	listeners.add(listener);
	return () => listeners.delete(listener);
}

/**
 * Persist a new map and tell this window's readers.
 *
 * A storage that refuses to write is survivable: the choice is lost, the pets are
 * not. This is decoration running on someone's desktop, and there is no state it
 * can be in that is worth an exception for.
 */
function commit(next: StoredLooks) {
	try {
		// ⚠ BOTH halves, always. Writing one would erase the other, and the two are
		// answers to different questions: which session this is, and which project.
		storage()?.setItem(LOOKS_STORAGE_KEY, serializeStoredLooks(next));
	} catch {
		// Private mode, a full quota, a locked-down profile. Keep going.
	}
	cache = next;
	for (const listener of listeners) listener();
}

/** Choose one axis for one session. Every other axis stays as it was. */
export function storeLookChoice(sessionRef: string, axisId: AxisId, optionId: string): void {
	const stored = readStoredLooks();
	commit({ ...stored, sessions: chooseLook(stored.sessions, sessionRef, axisId, optionId) });
}

/** Choose the creature a whole PROJECT is drawn as. */
export function storeProjectSpecies(project: string, species: SpeciesId): void {
	const stored = readStoredLooks();
	commit({ ...stored, projects: chooseSpecies(stored.projects, project, species) });
}

/** Put a project's creature back on the hash of its name. */
export function clearStoredProjectSpecies(project: string): void {
	const stored = readStoredLooks();
	const projects = clearSpeciesChoice(stored.projects, project);
	if (projects === stored.projects) return;
	commit({ ...stored, projects });
}

/** Put one axis - or with no axis, the whole session - back on the hash default. */
export function clearStoredLookChoice(sessionRef: string, axisId?: AxisId): void {
	const stored = readStoredLooks();
	const sessions = clearLookChoice(stored.sessions, sessionRef, axisId);
	if (sessions === stored.sessions) return;
	commit({ ...stored, sessions });
}

/**
 * Forget every session not in `liveRefs`.
 *
 * ⚠ Call this only from the MAIN APP, which has the authoritative session list. The
 * overlay shows at most `MAX_PETS`, so pruning against what it can see would delete
 * the saved look of a session that exists and is merely off the band.
 */
export function pruneStoredLooks(liveRefs: Iterable<string>, liveProjects?: Iterable<string>): void {
	const stored = readStoredLooks();
	const sessions = pruneLookOverrides(stored.sessions, liveRefs);
	const projects = liveProjects ? pruneProjectLooks(stored.projects, liveProjects) : stored.projects;
	if (sessions === stored.sessions && projects === stored.projects) return;
	commit({ sessions, projects });
}

/** The chosen looks, as React state. Repaints when either window changes them. */
export function useLookOverrides(): LookOverrides {
	return useSyncExternalStore(subscribeLookOverrides, readLookOverrides, readLookOverrides);
}

/** The projects' chosen creatures, as React state. */
export function useProjectLooks(): ProjectLooks {
	return useSyncExternalStore(subscribeLookOverrides, readProjectLooks, readProjectLooks);
}
