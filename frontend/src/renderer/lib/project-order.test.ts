import { describe, expect, it } from "vitest";
import { moveProject, orderWorkspaces } from "./project-order";

const ws = (id: string) => ({ id, name: id });

describe("orderWorkspaces", () => {
	it("returns the list unchanged when no order is saved", () => {
		const list = [ws("a"), ws("b"), ws("c")];
		expect(orderWorkspaces(list, []).map((w) => w.id)).toEqual(["a", "b", "c"]);
	});

	it("applies the saved order", () => {
		const list = [ws("a"), ws("b"), ws("c")];
		expect(orderWorkspaces(list, ["c", "a", "b"]).map((w) => w.id)).toEqual(["c", "a", "b"]);
	});

	it("appends new projects (not in the saved order) after the known ones, in incoming order", () => {
		const list = [ws("a"), ws("b"), ws("new1"), ws("new2")];
		expect(orderWorkspaces(list, ["b", "a"]).map((w) => w.id)).toEqual(["b", "a", "new1", "new2"]);
	});

	it("skips saved ids no longer present (removed projects)", () => {
		const list = [ws("a"), ws("c")];
		expect(orderWorkspaces(list, ["c", "gone", "a"]).map((w) => w.id)).toEqual(["c", "a"]);
	});

	it("does not mutate the input array", () => {
		const list = [ws("a"), ws("b")];
		orderWorkspaces(list, ["b", "a"]);
		expect(list.map((w) => w.id)).toEqual(["a", "b"]);
	});
});

describe("moveProject", () => {
	const ids = ["a", "b", "c", "d"];

	it("moves an item up to the top edge of a target", () => {
		expect(moveProject(ids, "d", "a", "top")).toEqual(["d", "a", "b", "c"]);
	});

	it("moves an item down to the bottom edge of a target", () => {
		expect(moveProject(ids, "a", "c", "bottom")).toEqual(["b", "c", "a", "d"]);
	});

	it("drops onto the top edge of a lower neighbour", () => {
		expect(moveProject(ids, "b", "c", "top")).toEqual(["a", "b", "c", "d"]);
	});

	it("returns the sequence unchanged when dropping onto itself", () => {
		expect(moveProject(ids, "b", "b", "top")).toEqual(ids);
	});

	it("returns the sequence unchanged for an unknown target", () => {
		expect(moveProject(ids, "a", "zzz", "bottom")).toEqual(ids);
	});
});
