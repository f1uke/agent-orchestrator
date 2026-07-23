import { useSyncExternalStore } from "react";
import type { AxisId } from "./cast";
import {
	chooseLook,
	clearLookChoice,
	LOOKS_STORAGE_KEY,
	parseLookOverrides,
	pruneLookOverrides,
	serializeLookOverrides,
	type LookOverrides,
} from "./look-store";

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

let cache: LookOverrides | null = null;
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

/** Every chosen look. Stable by identity until something actually changes. */
export function readLookOverrides(): LookOverrides {
	if (cache === null) cache = parseLookOverrides(storage()?.getItem(LOOKS_STORAGE_KEY) ?? null);
	return cache;
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
function commit(next: LookOverrides) {
	try {
		storage()?.setItem(LOOKS_STORAGE_KEY, serializeLookOverrides(next));
	} catch {
		// Private mode, a full quota, a locked-down profile. Keep going.
	}
	cache = next;
	for (const listener of listeners) listener();
}

/** Choose one axis for one session. Every other axis stays as it was. */
export function storeLookChoice(sessionRef: string, axisId: AxisId, optionId: string): void {
	commit(chooseLook(readLookOverrides(), sessionRef, axisId, optionId));
}

/** Put one axis - or with no axis, the whole session - back on the hash default. */
export function clearStoredLookChoice(sessionRef: string, axisId?: AxisId): void {
	const next = clearLookChoice(readLookOverrides(), sessionRef, axisId);
	if (next === readLookOverrides()) return;
	commit(next);
}

/**
 * Forget every session not in `liveRefs`.
 *
 * ⚠ Call this only from the MAIN APP, which has the authoritative session list. The
 * overlay shows at most `MAX_PETS`, so pruning against what it can see would delete
 * the saved look of a session that exists and is merely off the band.
 */
export function pruneStoredLooks(liveRefs: Iterable<string>): void {
	const current = readLookOverrides();
	const next = pruneLookOverrides(current, liveRefs);
	if (next === current) return;
	commit(next);
}

/** The chosen looks, as React state. Repaints when either window changes them. */
export function useLookOverrides(): LookOverrides {
	return useSyncExternalStore(subscribeLookOverrides, readLookOverrides, readLookOverrides);
}
