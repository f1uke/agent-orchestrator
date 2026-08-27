import { beforeEach, describe, expect, it } from "vitest";
import {
	type FilesPanelState,
	defaultState,
	readFilesPanelState,
	writeFilesPanelState,
	writeGlobalMode,
	writeGlobalView,
} from "./files-panel-state";

const arranged = (over: Partial<FilesPanelState> = {}): FilesPanelState => ({
	mode: "browse",
	view: "tree",
	browseExpanded: ["App", "App/Wallet"],
	changesCollapsed: ["backend"],
	browseScroll: 420,
	changesScroll: 0,
	...over,
});

beforeEach(() => {
	window.localStorage.clear();
});

describe("readFilesPanelState", () => {
	it("falls back to the global habit for a task nobody has arranged", () => {
		writeGlobalMode("browse");
		writeGlobalView("list");
		expect(readFilesPanelState("task-1")).toEqual({
			mode: "browse",
			view: "list",
			browseExpanded: [],
			changesCollapsed: [],
			browseScroll: 0,
			changesScroll: 0,
		});
	});

	// Changes is the default a fresh install opens in: a worker's rail is opened
	// to see what the agent did far more often than to go looking for a file.
	it("defaults to Changes and the tree with nothing stored at all", () => {
		expect(defaultState()).toMatchObject({ mode: "changes", view: "tree" });
	});

	it("gives each task its own arrangement back", () => {
		writeFilesPanelState("task-1", arranged());
		writeFilesPanelState("task-2", arranged({ mode: "changes", browseExpanded: ["Vendor"] }));
		expect(readFilesPanelState("task-1")).toMatchObject({ mode: "browse", browseExpanded: ["App", "App/Wallet"] });
		expect(readFilesPanelState("task-2")).toMatchObject({ mode: "changes", browseExpanded: ["Vendor"] });
	});

	// An entry written by an older build is missing whatever that build did not
	// have. Losing the folds because a scroll offset is absent would be a silly
	// way to forget.
	it("fills in field by field rather than discarding a partial entry", () => {
		window.localStorage.setItem("ao.files.state", JSON.stringify({ "task-1": { browseExpanded: ["App"] } }));
		expect(readFilesPanelState("task-1")).toMatchObject({ mode: "changes", browseExpanded: ["App"], browseScroll: 0 });
	});

	it("survives a corrupt or hostile blob", () => {
		window.localStorage.setItem("ao.files.state", "{not json");
		expect(readFilesPanelState("task-1")).toEqual(defaultState());
		window.localStorage.setItem("ao.files.state", JSON.stringify({ "task-1": { browseExpanded: "App", mode: 7 } }));
		expect(readFilesPanelState("task-1")).toMatchObject({ mode: "changes", browseExpanded: [] });
	});
});

describe("writeFilesPanelState", () => {
	// An app that ran for a year would otherwise grow this blob forever, and
	// nobody would ever see it happen.
	it("keeps the 40 most recently touched tasks", () => {
		for (let i = 0; i < 45; i++) writeFilesPanelState(`task-${i}`, arranged(), 1000 + i);
		const stored = JSON.parse(window.localStorage.getItem("ao.files.state") ?? "{}");
		expect(Object.keys(stored)).toHaveLength(40);
		expect(stored["task-0"]).toBeUndefined();
		expect(stored["task-44"]).toBeDefined();
	});

	it("touching a task keeps it out of the eviction", () => {
		for (let i = 0; i < 40; i++) writeFilesPanelState(`task-${i}`, arranged(), 1000 + i);
		writeFilesPanelState("task-0", arranged(), 9999);
		writeFilesPanelState("task-new", arranged(), 10000);
		const stored = JSON.parse(window.localStorage.getItem("ao.files.state") ?? "{}");
		expect(stored["task-0"]).toBeDefined();
		expect(stored["task-1"]).toBeUndefined();
	});

	it("caps a fold set at its newest 500 keys", () => {
		const many = Array.from({ length: 600 }, (_, i) => `dir-${i}`);
		writeFilesPanelState("task-1", arranged({ browseExpanded: many }));
		const restored = readFilesPanelState("task-1");
		expect(restored.browseExpanded).toHaveLength(500);
		expect(restored.browseExpanded[499]).toBe("dir-599");
	});

	it("never stores a negative or fractional scroll offset", () => {
		writeFilesPanelState("task-1", arranged({ browseScroll: -20, changesScroll: 17.6 }));
		expect(readFilesPanelState("task-1")).toMatchObject({ browseScroll: 0, changesScroll: 18 });
	});
});
