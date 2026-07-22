import { beforeEach, describe, expect, it } from "vitest";
import { leaf, type SplitNode } from "../lib/split-layout";
import { useUiStore } from "./ui-store";

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
