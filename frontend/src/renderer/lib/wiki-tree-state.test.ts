import { beforeEach, describe, expect, it } from "vitest";
import { defaultOpen, loadFolderState, loadSortOrder, saveFolderState, saveSortOrder } from "./wiki-tree-state";

const KEY = "ao.wiki.collapsed";

beforeEach(() => {
	window.localStorage.clear();
});

describe("defaultOpen", () => {
	it("opens the top level and shuts everything deeper", () => {
		expect(defaultOpen("Projects", 0, null)).toBe(true);
		expect(defaultOpen("Projects/MOBILITY-4713", 1, null)).toBe(false);
	});

	it("opens the folders on the way to the note being read", () => {
		expect(defaultOpen("Projects/MOBILITY-4713", 1, "Projects/MOBILITY-4713/_tasks.md")).toBe(true);
		// A folder whose NAME merely prefixes the open note's path is not on the
		// way to it — the separator is what makes it an ancestor.
		expect(defaultOpen("Projects/MOBILITY", 1, "Projects/MOBILITY-4713/_tasks.md")).toBe(false);
	});
});

describe("folder state", () => {
	it("round-trips a folder the reader shut", () => {
		saveFolderState("Projects", false);
		expect(loadFolderState()).toEqual({ Projects: false });
	});

	it("remembers only the folders that were touched", () => {
		saveFolderState("Projects/MOBILITY-4713", true);
		const state = loadFolderState();
		expect(state["Projects/MOBILITY-4713"]).toBe(true);
		expect("Projects" in state).toBe(false);
	});

	it("survives corrupt or foreign storage rather than throwing", () => {
		window.localStorage.setItem(KEY, "{not json");
		expect(loadFolderState()).toEqual({});
		window.localStorage.setItem(KEY, JSON.stringify({ a: 7, b: { open: "yes" }, c: { open: true } }));
		expect(loadFolderState()).toEqual({ c: true });
	});

	it("evicts the least recently touched folder past the cap", () => {
		for (let i = 0; i < 405; i += 1) saveFolderState(`folder-${i}`, false, 1000 + i);
		const state = loadFolderState();
		expect(Object.keys(state)).toHaveLength(400);
		expect("folder-0" in state).toBe(false);
		expect("folder-404" in state).toBe(true);
	});

	it("ignores the vault root, which has no row to remember", () => {
		saveFolderState("", false);
		expect(window.localStorage.getItem(KEY)).toBeNull();
	});
});

describe("sort order", () => {
	it("starts in the tree's own order", () => {
		expect(loadSortOrder()).toBe("asc");
	});

	it("round-trips the direction the reader chose", () => {
		saveSortOrder("desc");
		expect(window.localStorage.getItem("ao.wiki.sort")).toBe("desc");
		expect(loadSortOrder()).toBe("desc");
		saveSortOrder("asc");
		expect(loadSortOrder()).toBe("asc");
	});

	it("reads a foreign value as the default rather than trusting it", () => {
		window.localStorage.setItem("ao.wiki.sort", "sideways");
		expect(loadSortOrder()).toBe("asc");
	});

	it("survives the folder store filling up: its own key cannot be evicted", () => {
		saveSortOrder("desc");
		for (let i = 0; i < 405; i += 1) saveFolderState(`folder-${i}`, false, 1000 + i);
		expect(loadSortOrder()).toBe("desc");
	});
});
