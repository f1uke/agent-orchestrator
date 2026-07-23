import { useSyncExternalStore } from "react";
import {
	chooseSpecies,
	clearSpeciesChoice,
	LOOKS_STORAGE_KEY,
	parseProjectLooks,
	pruneProjectLooks,
	serializeProjectLooks,
	type ProjectLooks,
} from "./look-store";
import type { SpeciesId } from "./species";

// The chosen creatures, bound to localStorage and shared by BOTH windows.
//
// The app (`index.html`) and the overlay (`companion.html`) are two pages of the same
// origin: `app://renderer`, a scheme registered `standard` and `secure`, so it has a real
// origin rather than file://'s opaque null. That means one localStorage between them, and
// a `storage` event whenever the other window writes. Picking a creature in Settings
// therefore reaches the desktop with no IPC, no daemon round trip and nothing to keep in
// sync - there is one value and both windows read it.
//
// localStorage is the single source of truth, and `refreshProjectLooks` exists so the main
// process can NUDGE a re-read without carrying any data. Two channels that both mean "go
// and look again" cannot disagree; the slower one simply finds the work already done.
//
// The snapshot is cached because `useSyncExternalStore` compares by identity: a fresh
// object per read renders for ever.

let cache: ProjectLooks | null = null;
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
export function refreshProjectLooks(): void {
	announce();
}

// A `storage` event fires in every OTHER document on the origin, never the one that wrote.
// `key === null` is what a whole-storage clear reports, and it has to count.
if (typeof window !== "undefined") {
	window.addEventListener("storage", (event) => {
		if (event.key !== null && event.key !== LOOKS_STORAGE_KEY) return;
		announce();
	});
}

/** Every project's chosen creature. Stable by identity until something actually changes. */
export function readProjectLooks(): ProjectLooks {
	if (cache === null) cache = parseProjectLooks(storage()?.getItem(LOOKS_STORAGE_KEY) ?? null);
	return cache;
}

export function subscribeProjectLooks(listener: () => void): () => void {
	listeners.add(listener);
	return () => listeners.delete(listener);
}

/**
 * Persist a new map and tell this window's readers.
 *
 * A storage that refuses to write is survivable: the choice is lost, the pets are not.
 * This is decoration running on someone's desktop, and there is no state it can be in that
 * is worth an exception for.
 */
function commit(next: ProjectLooks) {
	try {
		storage()?.setItem(LOOKS_STORAGE_KEY, serializeProjectLooks(next));
	} catch {
		// Private mode, a full quota, a locked-down profile. Keep going.
	}
	cache = next;
	for (const listener of listeners) listener();
}

/** Choose the creature a whole PROJECT is drawn as. */
export function storeProjectSpecies(project: string, species: SpeciesId): void {
	commit(chooseSpecies(readProjectLooks(), project, species));
}

/** Put a project's creature back on the hash of its name. */
export function clearStoredProjectSpecies(project: string): void {
	const before = readProjectLooks();
	const next = clearSpeciesChoice(before, project);
	if (next === before) return;
	commit(next);
}

/**
 * Forget every project not in `liveNames`.
 *
 * ⚠ Call this only from the MAIN APP, which has the authoritative project list.
 */
export function pruneStoredProjectLooks(liveNames: Iterable<string>): void {
	const before = readProjectLooks();
	const next = pruneProjectLooks(before, liveNames);
	if (next === before) return;
	commit(next);
}

/** The projects' chosen creatures, as React state. Repaints when either window changes them. */
export function useProjectLooks(): ProjectLooks {
	return useSyncExternalStore(subscribeProjectLooks, readProjectLooks, readProjectLooks);
}
