import { beforeEach, describe, expect, it, vi } from "vitest";

describe("releases UI layers", () => {
	beforeEach(() => {
		vi.resetModules();
		window.localStorage.clear();
	});

	it("starts from the default layers and remembers a toggle across a reload", async () => {
		const first = await import("./releases-ui");
		expect(first.useReleasesUi.getState().layers).toEqual({
			sprint: false,
			llb: true,
			cards: true,
			past: false,
			src: false,
		});
		first.useReleasesUi.getState().setLayers({ ...first.useReleasesUi.getState().layers, past: true, src: true });
		vi.resetModules();
		const reloaded = await import("./releases-ui");
		expect(reloaded.useReleasesUi.getState().layers.past).toBe(true);
		expect(reloaded.useReleasesUi.getState().layers.src).toBe(true);
	});

	it("keeps folds open across a reload and closes them on a second toggle", async () => {
		const first = await import("./releases-ui");
		expect(first.useReleasesUi.getState().open.has("di:21-2026")).toBe(false);
		first.useReleasesUi.getState().toggleFold("di:21-2026");
		first.useReleasesUi.getState().openFolds(["di:llbs", "sp-DEMO"]);
		vi.resetModules();
		const reloaded = await import("./releases-ui");
		const open = reloaded.useReleasesUi.getState().open;
		expect([...open].sort()).toEqual(["di:21-2026", "di:llbs", "sp-DEMO"]);
		reloaded.useReleasesUi.getState().toggleFold("di:llbs");
		expect(reloaded.useReleasesUi.getState().open.has("di:llbs")).toBe(false);
	});

	it("starts with every fold closed when the stored folds are junk or storage throws", async () => {
		window.localStorage.setItem("ao.releases.folds.v1", '{"a":1}');
		const { useReleasesUi } = await import("./releases-ui");
		expect(useReleasesUi.getState().open.size).toBe(0);
		const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
			throw new Error("blocked");
		});
		useReleasesUi.getState().toggleFold("pa:22-2026");
		expect(useReleasesUi.getState().open.has("pa:22-2026")).toBe(true);
		set.mockRestore();
	});

	it("falls back to the defaults when storage holds junk or throws", async () => {
		window.localStorage.setItem("ao.releases.layers.v1", "{not json");
		const { useReleasesUi } = await import("./releases-ui");
		expect(useReleasesUi.getState().layers.cards).toBe(true);

		vi.resetModules();
		const spy = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
			throw new Error("blocked");
		});
		const blocked = await import("./releases-ui");
		expect(blocked.useReleasesUi.getState().layers.llb).toBe(true);
		spy.mockRestore();
		const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
			throw new Error("blocked");
		});
		blocked.useReleasesUi.getState().setLayers({ ...blocked.useReleasesUi.getState().layers, sprint: true });
		expect(blocked.useReleasesUi.getState().layers.sprint).toBe(true);
		set.mockRestore();
	});
});

describe("hidden sprint boards", () => {
	beforeEach(() => {
		vi.resetModules();
		window.localStorage.clear();
	});

	it("starts with every board shown and remembers the hidden set across a reload", async () => {
		const first = await import("./releases-ui");
		expect(first.useReleasesUi.getState().hidden.size).toBe(0);
		first.useReleasesUi.getState().setHidden(new Set(["PILOT", "DEMO"]));
		vi.resetModules();
		const reloaded = await import("./releases-ui");
		expect([...reloaded.useReleasesUi.getState().hidden].sort()).toEqual(["DEMO", "PILOT"]);
		reloaded.useReleasesUi.getState().setHidden(new Set());
		vi.resetModules();
		expect((await import("./releases-ui")).useReleasesUi.getState().hidden.size).toBe(0);
	});

	it("shows every board when the stored set is junk or storage throws", async () => {
		window.localStorage.setItem("ao.releases.hidden-boards.v1", '{"DEMO":true}');
		const { useReleasesUi } = await import("./releases-ui");
		expect(useReleasesUi.getState().hidden.size).toBe(0);
		window.localStorage.setItem("ao.releases.hidden-boards.v1", '["DEMO", 7, ""]');
		vi.resetModules();
		expect([...(await import("./releases-ui")).useReleasesUi.getState().hidden]).toEqual(["DEMO"]);
		const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
			throw new Error("blocked");
		});
		useReleasesUi.getState().setHidden(new Set(["PILOT"]));
		expect(useReleasesUi.getState().hidden.has("PILOT")).toBe(true);
		set.mockRestore();
	});
});

describe("releases page memory", () => {
	beforeEach(() => {
		vi.resetModules();
		window.localStorage.clear();
	});

	it("keeps the last search and scroll across a reload", async () => {
		const first = await import("./releases-ui");
		first.saveSearch({ apps: "di", me: "1", sheet: "attention", layers: "sprint,cards" });
		first.useReleasesUi.getState().setScroll({ left: 700, top: 120, focus: "21-2026" });
		vi.resetModules();
		const reloaded = await import("./releases-ui");
		expect(reloaded.readSavedSearch()).toEqual({ apps: "di", me: "1", sheet: "attention", layers: "sprint,cards" });
		expect(reloaded.useReleasesUi.getState().scroll).toEqual({ left: 700, top: 120, focus: "21-2026" });
	});

	it("drops what the URL parser would refuse and survives junk or blocked storage", async () => {
		window.localStorage.setItem("ao.releases.search.v1", JSON.stringify({ sheet: "nope:", me: "maybe", apps: "di" }));
		window.localStorage.setItem("ao.releases.scroll.v1", '{"left":"far"}');
		const { readSavedSearch, useReleasesUi, saveSearch } = await import("./releases-ui");
		expect(readSavedSearch()).toEqual({ apps: "di" });
		expect(useReleasesUi.getState().scroll).toBeNull();
		window.localStorage.setItem("ao.releases.search.v1", "{not json");
		expect(readSavedSearch()).toEqual({});
		const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
			throw new Error("blocked");
		});
		saveSearch({ apps: "da" });
		useReleasesUi.getState().setScroll({ left: 1, top: 2, focus: null });
		expect(useReleasesUi.getState().scroll).toEqual({ left: 1, top: 2, focus: null });
		set.mockRestore();
	});
});
