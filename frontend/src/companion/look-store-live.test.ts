import { beforeEach, describe, expect, it, vi } from "vitest";
import { LOOKS_STORAGE_KEY, resolveSpecies, serializeProjectLooks } from "./look-store";
import {
	clearStoredProjectSpecies,
	pruneStoredProjectLooks,
	readProjectLooks,
	refreshProjectLooks,
	storeProjectSpecies,
	subscribeProjectLooks,
} from "./look-store-live";
import { speciesForProject } from "./species";

const PROJECT = "agent-orchestrator";
const OTHER = "starlight";

/** What the OTHER window writing looks like from in here. */
function otherWindowWrote(value: string | null) {
	if (value === null) window.localStorage.removeItem(LOOKS_STORAGE_KEY);
	else window.localStorage.setItem(LOOKS_STORAGE_KEY, value);
	window.dispatchEvent(new StorageEvent("storage", { key: LOOKS_STORAGE_KEY, newValue: value }));
}

beforeEach(() => {
	window.localStorage.clear();
	// A `storage` event with a null key is what `localStorage.clear()` reports, and it is
	// also how this suite drops the module's cached snapshot between tests.
	window.dispatchEvent(new StorageEvent("storage", { key: null }));
});

describe("reading", () => {
	it("starts out with nobody having chosen anything", () => {
		expect(readProjectLooks()).toEqual({});
	});

	it("reads what a previous run left behind, which is what surviving a restart means", () => {
		window.localStorage.setItem(LOOKS_STORAGE_KEY, serializeProjectLooks({ [PROJECT]: "ghost" }));
		refreshProjectLooks();

		expect(resolveSpecies(PROJECT, readProjectLooks())).toBe("ghost");
	});

	it("hands back the SAME object until something changes", () => {
		// `useSyncExternalStore` compares snapshots by identity. A fresh object per read is
		// an infinite render loop, not a slow one.
		expect(readProjectLooks()).toBe(readProjectLooks());

		const before = readProjectLooks();
		storeProjectSpecies(PROJECT, "ghost");

		expect(readProjectLooks()).not.toBe(before);
	});
});

describe("writing", () => {
	it("puts a choice where the next launch will find it", () => {
		storeProjectSpecies(PROJECT, "ghost");

		const stored = window.localStorage.getItem(LOOKS_STORAGE_KEY);
		expect(stored).not.toBeNull();
		expect(JSON.parse(stored!).projects).toEqual({ [PROJECT]: "ghost" });
	});

	it("tells everyone watching", () => {
		const listener = vi.fn();
		const stop = subscribeProjectLooks(listener);

		storeProjectSpecies(PROJECT, "slime");

		expect(listener).toHaveBeenCalled();
		stop();
	});

	it("puts a project back on the hash", () => {
		storeProjectSpecies(PROJECT, "ghost");
		clearStoredProjectSpecies(PROJECT);

		expect(resolveSpecies(PROJECT, readProjectLooks())).toBe(speciesForProject(PROJECT));
	});

	it("writes nothing when there was no choice to clear", () => {
		const setItem = vi.spyOn(window.localStorage, "setItem");

		clearStoredProjectSpecies(PROJECT);

		expect(setItem).not.toHaveBeenCalled();
		setItem.mockRestore();
	});

	it("stops telling a listener that has unsubscribed", () => {
		const listener = vi.fn();
		subscribeProjectLooks(listener)();

		storeProjectSpecies(PROJECT, "ghost");

		expect(listener).not.toHaveBeenCalled();
	});

	it("survives a storage that refuses to write", () => {
		// Private mode, a full quota, a locked-down profile. Losing the choice is
		// acceptable; taking the whole overlay down with an exception is not.
		const setItem = vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
			throw new Error("nope");
		});

		expect(() => storeProjectSpecies(PROJECT, "ghost")).not.toThrow();
		setItem.mockRestore();
	});
});

describe("the other window", () => {
	it("sees a change made in the app, which is how the overlay repaints", () => {
		// The app and the overlay are two windows on ONE origin (app://renderer), so this
		// event is the entire cross-window channel. If it ever stopped firing, the pets
		// would not change until the overlay reloaded.
		const listener = vi.fn();
		const stop = subscribeProjectLooks(listener);

		otherWindowWrote(serializeProjectLooks({ [PROJECT]: "cat" }));

		expect(listener).toHaveBeenCalled();
		expect(resolveSpecies(PROJECT, readProjectLooks())).toBe("cat");
		stop();
	});

	it("ignores a change to some unrelated key", () => {
		const listener = vi.fn();
		const stop = subscribeProjectLooks(listener);

		window.dispatchEvent(new StorageEvent("storage", { key: "ao.theme", newValue: "light" }));

		expect(listener).not.toHaveBeenCalled();
		stop();
	});

	it("treats a whole-storage clear as ours changing", () => {
		storeProjectSpecies(PROJECT, "ghost");
		const listener = vi.fn();
		const stop = subscribeProjectLooks(listener);

		window.localStorage.clear();
		window.dispatchEvent(new StorageEvent("storage", { key: null }));

		expect(listener).toHaveBeenCalled();
		expect(readProjectLooks()).toEqual({});
		stop();
	});

	it("re-reads on a nudge, so the overlay does not depend on one channel alone", () => {
		// `refreshProjectLooks` is what the main process's content-free IPC ping calls.
		// localStorage stays the single source of truth either way, so the two channels
		// cannot disagree - the slower one just finds the work already done.
		const listener = vi.fn();
		const stop = subscribeProjectLooks(listener);

		window.localStorage.setItem(LOOKS_STORAGE_KEY, serializeProjectLooks({ [PROJECT]: "chick" }));
		refreshProjectLooks();

		expect(listener).toHaveBeenCalled();
		expect(resolveSpecies(PROJECT, readProjectLooks())).toBe("chick");
		stop();
	});
});

describe("pruning", () => {
	it("forgets a project that no longer exists", () => {
		storeProjectSpecies(PROJECT, "ghost");
		storeProjectSpecies(OTHER, "cat");

		pruneStoredProjectLooks([PROJECT]);

		expect(Object.keys(readProjectLooks())).toEqual([PROJECT]);
	});

	it("writes nothing when there is nothing to forget", () => {
		storeProjectSpecies(PROJECT, "ghost");
		const setItem = vi.spyOn(window.localStorage, "setItem");

		pruneStoredProjectLooks([PROJECT, OTHER]);

		expect(setItem).not.toHaveBeenCalled();
		setItem.mockRestore();
	});
});
