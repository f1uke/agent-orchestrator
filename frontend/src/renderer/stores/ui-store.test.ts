import { beforeEach, describe, expect, it } from "vitest";
import { useUiStore } from "./ui-store";

const STORAGE_KEY = "ao.projects.collapsed";

beforeEach(() => {
	localStorage.clear();
	useUiStore.setState({ collapsedProjectIds: new Set() });
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
