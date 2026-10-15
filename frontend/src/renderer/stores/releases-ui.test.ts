import { beforeEach, describe, expect, it, vi } from "vitest";

describe("releases UI layers", () => {
	beforeEach(() => {
		vi.resetModules();
		window.localStorage.clear();
	});

	it("starts from the default layers and remembers a toggle across a reload", async () => {
		const first = await import("./releases-ui");
		expect(first.useReleasesUi.getState().layers).toEqual({ sprint: false, llb: true, cards: true, past: false });
		first.useReleasesUi.getState().setLayer("past", true);
		vi.resetModules();
		const reloaded = await import("./releases-ui");
		expect(reloaded.useReleasesUi.getState().layers.past).toBe(true);
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
		blocked.useReleasesUi.getState().setLayer("sprint", true);
		expect(blocked.useReleasesUi.getState().layers.sprint).toBe(true);
		set.mockRestore();
	});
});
