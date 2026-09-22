import { beforeEach, describe, expect, it } from "vitest";
import { leaf, type SplitNode } from "../lib/split-layout";
import { readFoldedBoardLanes, useUiStore } from "./ui-store";

const STORAGE_KEY = "ao.projects.collapsed";
const ORDER_STORAGE_KEY = "ao.projects.order";
const SPLIT_STORAGE_KEY = "ao.split.layouts";

beforeEach(() => {
	localStorage.clear();
	useUiStore.setState({ collapsedProjectIds: new Set(), projectOrder: [], splitLayouts: {} });
});

describe("ui-store per-project collapse", () => {
	it("defaults to no collapsed projects (all expanded)", () => {
		expect(useUiStore.getState().collapsedProjectIds.size).toBe(0);
	});

	it("toggleProjectCollapsed collapses then expands a project", () => {
		const { toggleProjectCollapsed } = useUiStore.getState();

		toggleProjectCollapsed("proj-1");
		expect(useUiStore.getState().collapsedProjectIds.has("proj-1")).toBe(true);

		toggleProjectCollapsed("proj-1");
		expect(useUiStore.getState().collapsedProjectIds.has("proj-1")).toBe(false);
	});

	it("persists the collapsed set to localStorage on each toggle", () => {
		useUiStore.getState().toggleProjectCollapsed("proj-1");
		expect(JSON.parse(localStorage.getItem(STORAGE_KEY)!)).toEqual(["proj-1"]);

		useUiStore.getState().toggleProjectCollapsed("proj-2");
		expect(new Set(JSON.parse(localStorage.getItem(STORAGE_KEY)!))).toEqual(new Set(["proj-1", "proj-2"]));

		useUiStore.getState().toggleProjectCollapsed("proj-1");
		expect(JSON.parse(localStorage.getItem(STORAGE_KEY)!)).toEqual(["proj-2"]);
	});
});

describe("ui-store project order", () => {
	it("defaults to the daemon order (empty custom order)", () => {
		expect(useUiStore.getState().projectOrder).toEqual([]);
	});

	it("setProjectOrder stores the order and persists it to localStorage", () => {
		useUiStore.getState().setProjectOrder(["proj-2", "proj-1", "proj-3"]);
		expect(useUiStore.getState().projectOrder).toEqual(["proj-2", "proj-1", "proj-3"]);
		expect(JSON.parse(localStorage.getItem(ORDER_STORAGE_KEY)!)).toEqual(["proj-2", "proj-1", "proj-3"]);
	});
});

describe("ui-store split layouts", () => {
	const tree: SplitNode = {
		kind: "split",
		orientation: "horizontal",
		ratio: 0.5,
		first: leaf("sess-a"),
		second: leaf("sess-b"),
	};

	it("defaults to no split layouts", () => {
		expect(useUiStore.getState().splitLayouts).toEqual({});
	});

	it("setSplitLayout stores a project's tree and persists the versioned map", () => {
		useUiStore.getState().setSplitLayout("proj-1", tree);
		expect(useUiStore.getState().splitLayouts["proj-1"]).toEqual(tree);
		expect(JSON.parse(localStorage.getItem(SPLIT_STORAGE_KEY)!)).toEqual({ v: 1, layouts: { "proj-1": tree } });
	});

	it("setSplitLayout(null) removes the project's layout and persists the removal", () => {
		useUiStore.getState().setSplitLayout("proj-1", tree);
		useUiStore.getState().setSplitLayout("proj-2", leaf("z"));
		useUiStore.getState().setSplitLayout("proj-1", null);
		expect(useUiStore.getState().splitLayouts).toEqual({ "proj-2": leaf("z") });
		expect(JSON.parse(localStorage.getItem(SPLIT_STORAGE_KEY)!)).toEqual({
			v: 1,
			layouts: { "proj-2": leaf("z") },
		});
	});

	it("removing an absent layout is a no-op", () => {
		useUiStore.getState().setSplitLayout("proj-1", null);
		expect(useUiStore.getState().splitLayouts).toEqual({});
	});
});

describe("ui-store board lane folding", () => {
	const LANES_KEY = "ao.board.collapsedLanes";

	it("starts with only Done folded", () => {
		expect([...readFoldedBoardLanes()]).toEqual(["done"]);
	});

	it("reads the array stored before Done was a lane as the lanes folded, Done still folded", () => {
		localStorage.setItem(LANES_KEY, JSON.stringify(["todo", "merge"]));
		expect(readFoldedBoardLanes()).toEqual(new Set(["done", "todo", "merge"]));
	});

	it("remembers a default-folded lane the human opened", () => {
		useUiStore.setState({ collapsedBoardLanes: readFoldedBoardLanes() });
		useUiStore.getState().toggleBoardLaneCollapsed("done");
		useUiStore.getState().toggleBoardLaneCollapsed("working");

		expect(JSON.parse(localStorage.getItem(LANES_KEY)!)).toEqual({ working: true, done: false });
		expect(readFoldedBoardLanes()).toEqual(new Set(["working"]));
	});

	it("falls back to the defaults on a value it cannot read", () => {
		localStorage.setItem(LANES_KEY, "{not json");
		expect(readFoldedBoardLanes()).toEqual(new Set(["done"]));
	});
});
