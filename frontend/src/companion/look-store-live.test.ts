import { beforeEach, describe, expect, it, vi } from "vitest";
import { defaultLook } from "./cast";
import { LOOKS_STORAGE_KEY, resolveLook, serializeLookOverrides } from "./look-store";
import {
	clearStoredLookChoice,
	pruneStoredLooks,
	readLookOverrides,
	refreshLookOverrides,
	storeLookChoice,
	subscribeLookOverrides,
} from "./look-store-live";

const REF = "agent-orchestrator-168";
const OTHER = "agent-orchestrator-169";

/** What the OTHER window writing looks like from in here. */
function otherWindowWrote(value: string | null) {
	if (value === null) window.localStorage.removeItem(LOOKS_STORAGE_KEY);
	else window.localStorage.setItem(LOOKS_STORAGE_KEY, value);
	window.dispatchEvent(new StorageEvent("storage", { key: LOOKS_STORAGE_KEY, newValue: value }));
}

beforeEach(() => {
	window.localStorage.clear();
	// A `storage` event with a null key is what `localStorage.clear()` reports, and
	// it is also how this suite drops the module's cached snapshot between tests.
	window.dispatchEvent(new StorageEvent("storage", { key: null }));
});

describe("reading", () => {
	it("starts out with nobody having chosen anything", () => {
		expect(readLookOverrides()).toEqual({});
	});

	it("reads what a previous run left behind, which is what surviving a restart means", () => {
		window.localStorage.setItem(LOOKS_STORAGE_KEY, serializeLookOverrides({ [REF]: { hat: "cone" } }));
		refreshLookOverrides();

		expect(resolveLook(REF, readLookOverrides()).hat).toBe("cone");
	});

	it("hands back the SAME object until something changes", () => {
		// `useSyncExternalStore` compares snapshots by identity. A fresh object per
		// read is an infinite render loop, not a slow one.
		expect(readLookOverrides()).toBe(readLookOverrides());

		const before = readLookOverrides();
		storeLookChoice(REF, "hat", "cone");

		expect(readLookOverrides()).not.toBe(before);
	});
});

describe("writing", () => {
	it("puts a choice where the next launch will find it", () => {
		storeLookChoice(REF, "hat", "cone");

		const stored = window.localStorage.getItem(LOOKS_STORAGE_KEY);
		expect(stored).not.toBeNull();
		expect(JSON.parse(stored!).sessions[REF]).toEqual({ hat: "cone" });
	});

	it("tells everyone watching", () => {
		const listener = vi.fn();
		const stop = subscribeLookOverrides(listener);

		storeLookChoice(REF, "palette", "mint");

		expect(listener).toHaveBeenCalled();
		stop();
	});

	it("puts an axis back on the hash", () => {
		storeLookChoice(REF, "hat", "cone");
		clearStoredLookChoice(REF, "hat");

		expect(resolveLook(REF, readLookOverrides())).toEqual(defaultLook(REF));
	});

	it("stops telling a listener that has unsubscribed", () => {
		const listener = vi.fn();
		subscribeLookOverrides(listener)();

		storeLookChoice(REF, "hat", "cone");

		expect(listener).not.toHaveBeenCalled();
	});

	it("survives a storage that refuses to write", () => {
		// Private mode, a full quota, a locked-down profile. Losing the choice is
		// acceptable; taking the whole overlay down with an exception is not.
		const setItem = vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
			throw new Error("nope");
		});

		expect(() => storeLookChoice(REF, "hat", "cone")).not.toThrow();
		setItem.mockRestore();
	});
});

describe("the other window", () => {
	it("sees a change made in the app, which is how the overlay repaints", () => {
		// The app and the overlay are two windows on ONE origin (app://renderer), so
		// this event is the entire cross-window channel. If it ever stopped firing,
		// the pet would not change until the overlay reloaded.
		const listener = vi.fn();
		const stop = subscribeLookOverrides(listener);

		otherWindowWrote(serializeLookOverrides({ [REF]: { palette: "mint" } }));

		expect(listener).toHaveBeenCalled();
		expect(resolveLook(REF, readLookOverrides()).palette).toBe("mint");
		stop();
	});

	it("ignores a change to some unrelated key", () => {
		const listener = vi.fn();
		const stop = subscribeLookOverrides(listener);

		window.dispatchEvent(new StorageEvent("storage", { key: "ao.theme", newValue: "light" }));

		expect(listener).not.toHaveBeenCalled();
		stop();
	});

	it("treats a whole-storage clear as ours changing", () => {
		storeLookChoice(REF, "hat", "cone");
		const listener = vi.fn();
		const stop = subscribeLookOverrides(listener);

		window.localStorage.clear();
		window.dispatchEvent(new StorageEvent("storage", { key: null }));

		expect(listener).toHaveBeenCalled();
		expect(readLookOverrides()).toEqual({});
		stop();
	});

	it("re-reads on a nudge, so the overlay does not depend on one channel alone", () => {
		// `refreshLookOverrides` is what the main process's content-free IPC ping
		// calls. localStorage stays the single source of truth either way, so the two
		// channels cannot disagree - the slower one just finds the work already done.
		const listener = vi.fn();
		const stop = subscribeLookOverrides(listener);

		window.localStorage.setItem(LOOKS_STORAGE_KEY, serializeLookOverrides({ [REF]: { hat: "bucket" } }));
		refreshLookOverrides();

		expect(listener).toHaveBeenCalled();
		expect(resolveLook(REF, readLookOverrides()).hat).toBe("bucket");
		stop();
	});
});

describe("pruning", () => {
	it("forgets a session that no longer exists", () => {
		storeLookChoice(REF, "hat", "cone");
		storeLookChoice(OTHER, "palette", "mint");

		pruneStoredLooks([REF]);

		expect(Object.keys(readLookOverrides())).toEqual([REF]);
	});

	it("writes nothing when there is nothing to forget", () => {
		storeLookChoice(REF, "hat", "cone");
		const setItem = vi.spyOn(window.localStorage, "setItem");

		pruneStoredLooks([REF, OTHER]);

		expect(setItem).not.toHaveBeenCalled();
		setItem.mockRestore();
	});
});
