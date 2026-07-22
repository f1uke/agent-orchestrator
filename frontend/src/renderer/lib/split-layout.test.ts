import { describe, expect, it } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import {
	addPane,
	applyDrop,
	containsSession,
	eligibleSplitSessions,
	leaf,
	MAX_SPLIT_PANES,
	MIN_PANE_HEIGHT,
	MIN_PANE_WIDTH,
	movePane,
	nearestPaneInDirection,
	paneCount,
	paneSessionIds,
	parseSplitLayouts,
	pruneToSessions,
	removePane,
	replaceSession,
	requiredExtent,
	resolveDropZone,
	serializeSplitLayouts,
	setRatioAtPath,
	SPLIT_HANDLE_SIZE,
	splitPane,
	swapPanes,
	type SplitNode,
} from "./split-layout";

// a | (b / c): two columns, the right column split vertically.
function threePane(): SplitNode {
	return {
		kind: "split",
		orientation: "horizontal",
		ratio: 0.5,
		first: leaf("a"),
		second: { kind: "split", orientation: "vertical", ratio: 0.5, first: leaf("b"), second: leaf("c") },
	};
}

function chainOf(n: number): SplitNode {
	let root: SplitNode = leaf("s0");
	for (let i = 1; i < n; i += 1) {
		root = splitPane(root, `s${i - 1}`, "right", `s${i}`);
	}
	return root;
}

describe("tree basics", () => {
	it("walks pane session ids in visual order", () => {
		expect(paneSessionIds(threePane())).toEqual(["a", "b", "c"]);
		expect(paneCount(threePane())).toBe(3);
		expect(containsSession(threePane(), "b")).toBe(true);
		expect(containsSession(threePane(), "zzz")).toBe(false);
	});
});

describe("splitPane", () => {
	it("splits a leaf to the right into a horizontal branch (target first, new second)", () => {
		const root = splitPane(leaf("a"), "a", "right", "b");
		expect(root).toEqual({
			kind: "split",
			orientation: "horizontal",
			ratio: 0.5,
			first: leaf("a"),
			second: leaf("b"),
		});
	});

	it("splits a leaf down into a vertical branch", () => {
		const root = splitPane(leaf("a"), "a", "down", "b");
		expect(root).toMatchObject({ kind: "split", orientation: "vertical" });
	});

	it("splits a nested target, leaving the rest of the tree untouched", () => {
		const root = splitPane(threePane(), "c", "down", "d");
		expect(paneSessionIds(root)).toEqual(["a", "b", "c", "d"]);
		expect(root).toMatchObject({
			second: { second: { kind: "split", orientation: "vertical", first: leaf("c"), second: leaf("d") } },
		});
	});

	it("no-ops when the target is missing or the session is already on screen", () => {
		const root = threePane();
		expect(splitPane(root, "zzz", "right", "d")).toBe(root);
		expect(splitPane(root, "a", "right", "b")).toBe(root);
	});

	it("no-ops at the pane cap", () => {
		const root = chainOf(MAX_SPLIT_PANES);
		expect(paneCount(root)).toBe(MAX_SPLIT_PANES);
		expect(splitPane(root, "s0", "right", "extra")).toBe(root);
	});
});

describe("addPane (navigation add)", () => {
	it("splits the focused pane to the right while below the cap", () => {
		const { root, mode } = addPane(threePane(), "b", "d");
		expect(mode).toBe("split");
		expect(paneSessionIds(root)).toEqual(["a", "b", "d", "c"]);
	});

	it("falls back to the last pane when the focused session is not in the tree", () => {
		const { root, mode } = addPane(threePane(), "gone", "d");
		expect(mode).toBe("split");
		expect(paneSessionIds(root)).toEqual(["a", "b", "c", "d"]);
	});

	it("swaps into the focused pane at the cap", () => {
		const full = chainOf(MAX_SPLIT_PANES);
		const { root, mode } = addPane(full, "s3", "fresh");
		expect(mode).toBe("swapped");
		expect(paneCount(root)).toBe(MAX_SPLIT_PANES);
		expect(containsSession(root, "fresh")).toBe(true);
		expect(containsSession(root, "s3")).toBe(false);
	});

	it("no-ops when the session is already in the tree", () => {
		const tree = threePane();
		const { root, mode } = addPane(tree, "a", "c");
		expect(mode).toBe("noop");
		expect(root).toBe(tree);
	});
});

describe("removePane", () => {
	it("removes a leaf and promotes its sibling", () => {
		const root = removePane(threePane(), "b");
		expect(root).toEqual({
			kind: "split",
			orientation: "horizontal",
			ratio: 0.5,
			first: leaf("a"),
			second: leaf("c"),
		});
	});

	it("returns null when the last pane is removed", () => {
		expect(removePane(leaf("a"), "a")).toBeNull();
	});

	it("returns the same tree when the session is not present", () => {
		const root = threePane();
		expect(removePane(root, "zzz")).toBe(root);
	});
});

describe("replaceSession", () => {
	it("swaps a session in place, preserving the structure", () => {
		const root = replaceSession(threePane(), "b", "x");
		expect(paneSessionIds(root)).toEqual(["a", "x", "c"]);
		expect(root).toMatchObject({ kind: "split", orientation: "horizontal" });
	});

	it("returns the same tree when the old session is missing", () => {
		const root = threePane();
		expect(replaceSession(root, "zzz", "x")).toBe(root);
	});
});

describe("pruneToSessions", () => {
	it("keeps only alive sessions, collapsing emptied branches", () => {
		const root = pruneToSessions(threePane(), new Set(["a", "c"]));
		expect(root).toEqual({
			kind: "split",
			orientation: "horizontal",
			ratio: 0.5,
			first: leaf("a"),
			second: leaf("c"),
		});
	});

	it("collapses to a single leaf", () => {
		expect(pruneToSessions(threePane(), new Set(["b"]))).toEqual(leaf("b"));
	});

	it("returns null when nothing survives", () => {
		expect(pruneToSessions(threePane(), new Set())).toBeNull();
	});

	it("returns the same tree when everything is alive", () => {
		const root = threePane();
		expect(pruneToSessions(root, new Set(["a", "b", "c"]))).toBe(root);
	});
});

describe("setRatioAtPath", () => {
	it("sets the ratio at the root and at a nested branch", () => {
		const atRoot = setRatioAtPath(threePane(), "", 0.7);
		expect(atRoot).toMatchObject({ ratio: 0.7 });
		const nested = setRatioAtPath(threePane(), "s", 0.3);
		expect(nested).toMatchObject({ ratio: 0.5, second: { ratio: 0.3 } });
	});

	it("clamps the ratio to sane bounds", () => {
		expect(setRatioAtPath(threePane(), "", 0)).toMatchObject({ ratio: 0.05 });
		expect(setRatioAtPath(threePane(), "", 1)).toMatchObject({ ratio: 0.95 });
	});

	it("returns the same tree for a path that does not name a branch", () => {
		const root = threePane();
		expect(setRatioAtPath(root, "ff", 0.4)).toBe(root);
		expect(setRatioAtPath(root, "f", 0.4)).toBe(root); // "f" is a leaf
	});
});

describe("requiredExtent", () => {
	it("is the pane floor for a leaf", () => {
		expect(requiredExtent(leaf("a"))).toEqual({ width: MIN_PANE_WIDTH, height: MIN_PANE_HEIGHT });
	});

	it("sums widths across a horizontal split and heights across a vertical one", () => {
		expect(requiredExtent(threePane())).toEqual({
			width: MIN_PANE_WIDTH * 2 + SPLIT_HANDLE_SIZE,
			height: MIN_PANE_HEIGHT * 2 + SPLIT_HANDLE_SIZE,
		});
	});
});

describe("serialize / parse", () => {
	it("round-trips a layouts map", () => {
		const layouts = { "proj-1": threePane(), "proj-2": leaf("z") };
		expect(parseSplitLayouts(serializeSplitLayouts(layouts))).toEqual(layouts);
	});

	it("returns empty for null, garbage, or an unknown version", () => {
		expect(parseSplitLayouts(null)).toEqual({});
		expect(parseSplitLayouts("not json")).toEqual({});
		expect(parseSplitLayouts(JSON.stringify({ v: 99, layouts: {} }))).toEqual({});
	});

	it("drops malformed project entries but keeps valid ones", () => {
		const raw = JSON.stringify({
			v: 1,
			layouts: {
				good: leaf("a"),
				badKind: { kind: "nope" },
				badRatio: { kind: "split", orientation: "horizontal", ratio: "x", first: leaf("a"), second: leaf("b") },
				badOrientation: { kind: "split", orientation: "diagonal", ratio: 0.5, first: leaf("a"), second: leaf("b") },
				badLeaf: { kind: "leaf", sessionId: 7 },
			},
		});
		expect(parseSplitLayouts(raw)).toEqual({ good: leaf("a") });
	});

	it("clamps out-of-range ratios while parsing", () => {
		const raw = JSON.stringify({
			v: 1,
			layouts: {
				p: { kind: "split", orientation: "horizontal", ratio: 4, first: leaf("a"), second: leaf("b") },
			},
		});
		expect(parseSplitLayouts(raw).p).toMatchObject({ ratio: 0.95 });
	});
});

describe("nearestPaneInDirection", () => {
	// 2x2 grid: a b / c d
	const rects = [
		{ sessionId: "a", rect: { left: 0, top: 0, width: 100, height: 100 } },
		{ sessionId: "b", rect: { left: 101, top: 0, width: 100, height: 100 } },
		{ sessionId: "c", rect: { left: 0, top: 101, width: 100, height: 100 } },
		{ sessionId: "d", rect: { left: 101, top: 101, width: 100, height: 100 } },
	];

	it("moves along rows and columns", () => {
		expect(nearestPaneInDirection(rects, "a", "right")).toBe("b");
		expect(nearestPaneInDirection(rects, "b", "left")).toBe("a");
		expect(nearestPaneInDirection(rects, "a", "down")).toBe("c");
		expect(nearestPaneInDirection(rects, "d", "up")).toBe("b");
	});

	it("returns null at an edge", () => {
		expect(nearestPaneInDirection(rects, "a", "left")).toBeNull();
		expect(nearestPaneInDirection(rects, "b", "up")).toBeNull();
	});

	it("prefers the aligned pane over a diagonal one", () => {
		expect(nearestPaneInDirection(rects, "c", "right")).toBe("d");
	});

	it("returns null when the from pane is unknown", () => {
		expect(nearestPaneInDirection(rects, "zzz", "right")).toBeNull();
	});
});

describe("eligibleSplitSessions", () => {
	const base = {
		workspaceId: "p",
		workspaceName: "p",
		provider: "claude-code",
		kind: "worker",
		branch: "b",
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
	};
	const sessions = [
		{ ...base, id: "live", title: "live", status: "working", kind: "worker" },
		{ ...base, id: "orch", title: "orch", status: "working", kind: "orchestrator" },
		{ ...base, id: "onscreen", title: "onscreen", status: "working" },
		{ ...base, id: "dead", title: "dead", status: "terminated", isTerminated: true },
		{ ...base, id: "queued", title: "queued", status: "todo", isTodo: true },
	] as WorkspaceSession[];

	it("offers live sessions (workers and the orchestrator), excluding terminated, todos, and on-screen panes", () => {
		const root = leaf("onscreen");
		expect(eligibleSplitSessions(sessions, root).map((s) => s.id)).toEqual(["live", "orch"]);
	});

	it("treats a missing tree as nothing on screen", () => {
		expect(eligibleSplitSessions(sessions, null).map((s) => s.id)).toEqual(["live", "orch", "onscreen"]);
	});
});

describe("swapPanes (pane drag, centre drop)", () => {
	it("exchanges two leaves' sessions, leaving the tree structure untouched", () => {
		const root = swapPanes(threePane(), "a", "c");
		// a<->c: structure identical, only the ids at those leaves swapped.
		expect(root).toEqual({
			kind: "split",
			orientation: "horizontal",
			ratio: 0.5,
			first: leaf("c"),
			second: { kind: "split", orientation: "vertical", ratio: 0.5, first: leaf("b"), second: leaf("a") },
		});
	});

	it("swaps immediate siblings without restructuring", () => {
		const root = swapPanes(threePane(), "b", "c");
		expect(root).toMatchObject({
			second: { kind: "split", orientation: "vertical", first: leaf("c"), second: leaf("b") },
		});
	});

	it("is a no-op onto itself or when a session is missing", () => {
		const root = threePane();
		expect(swapPanes(root, "a", "a")).toBe(root);
		expect(swapPanes(root, "a", "zzz")).toBe(root);
	});

	it("does not change the pane count (allowed at cap)", () => {
		const full = chainOf(MAX_SPLIT_PANES);
		const swapped = swapPanes(full, "s0", "s9");
		expect(paneCount(swapped)).toBe(MAX_SPLIT_PANES);
		expect(paneSessionIds(swapped).at(0)).toBe("s9");
		expect(paneSessionIds(swapped).at(-1)).toBe("s0");
	});
});

describe("movePane (pane drag, edge drop)", () => {
	it("detaches the dragged leaf, collapses its old parent, and re-splits at the target", () => {
		// a | (b / c): move a next to c, downward.
		const root = movePane(threePane(), "a", "c", "down");
		expect(paneSessionIds(root)).toEqual(["b", "c", "a"]);
		expect(root).toMatchObject({
			// a's old parent (the root split) collapses; the right column is promoted to root.
			kind: "split",
			orientation: "vertical",
			first: leaf("b"),
			second: { kind: "split", orientation: "vertical", first: leaf("c"), second: leaf("a") },
		});
	});

	it("moving onto an immediate sibling repositions without an orphan or single-child node", () => {
		// b and c are siblings in the right column; move b below c.
		const root = movePane(threePane(), "b", "c", "down");
		expect(paneSessionIds(root)).toEqual(["a", "c", "b"]);
		expect(root).toEqual({
			kind: "split",
			orientation: "horizontal",
			ratio: 0.5,
			first: leaf("a"),
			second: { kind: "split", orientation: "vertical", ratio: 0.5, first: leaf("c"), second: leaf("b") },
		});
	});

	it("moving one of two panes just flips their arrangement, staying a clean 2-leaf split", () => {
		const root = movePane(
			{ kind: "split", orientation: "horizontal", ratio: 0.5, first: leaf("a"), second: leaf("b") },
			"a",
			"b",
			"down",
		);
		expect(root).toEqual({
			kind: "split",
			orientation: "vertical",
			ratio: 0.5,
			first: leaf("b"),
			second: leaf("a"),
		});
	});

	it("is a no-op onto itself or when a session is missing", () => {
		const root = threePane();
		expect(movePane(root, "a", "a", "right")).toBe(root);
		expect(movePane(root, "a", "zzz", "right")).toBe(root);
		expect(movePane(root, "zzz", "a", "right")).toBe(root);
	});

	it("does NOT change the pane count — a move is allowed at the cap", () => {
		const full = chainOf(MAX_SPLIT_PANES);
		const moved = movePane(full, "s0", "s9", "down");
		expect(paneCount(moved)).toBe(MAX_SPLIT_PANES);
		expect(containsSession(moved, "s0")).toBe(true);
	});

	it("survives a serialise/restore round-trip", () => {
		const moved = movePane(threePane(), "a", "c", "down");
		const restored = parseSplitLayouts(serializeSplitLayouts({ p: moved })).p;
		expect(restored).toEqual(moved);
	});
});

describe("resolveDropZone", () => {
	it("session drags split the pane along the diagonal: upper-right → right, lower-left → down", () => {
		expect(resolveDropZone(0.9, 0.2, "session")).toBe("right");
		expect(resolveDropZone(0.2, 0.9, "session")).toBe("down");
		// No centre for a sidebar session — the whole pane resolves to right or down.
		expect(resolveDropZone(0.5, 0.5, "session")).not.toBe("center");
	});

	it("pane drags reserve a central swap zone, with right/bottom edge strips for moves", () => {
		expect(resolveDropZone(0.5, 0.5, "pane")).toBe("center");
		expect(resolveDropZone(0.9, 0.5, "pane")).toBe("right");
		expect(resolveDropZone(0.4, 0.9, "pane")).toBe("down");
		// The right strip owns the bottom-right corner (no ambiguous overlap).
		expect(resolveDropZone(0.9, 0.9, "pane")).toBe("right");
	});
});

describe("applyDrop", () => {
	it("sidebar session, right/down → split at the target", () => {
		const right = applyDrop(threePane(), { kind: "session", sessionId: "new" }, "b", "right");
		expect(paneSessionIds(right.root)).toEqual(["a", "b", "new", "c"]);
		expect(right.refused).toBeFalsy();

		const down = applyDrop(threePane(), { kind: "session", sessionId: "new" }, "b", "down");
		expect(paneSessionIds(down.root)).toEqual(["a", "b", "new", "c"]);
		expect(down.root).toMatchObject({
			second: { first: { kind: "split", orientation: "vertical", first: leaf("b"), second: leaf("new") } },
		});
	});

	it("sidebar session at the cap is REFUSED, not silently dropped", () => {
		const full = chainOf(MAX_SPLIT_PANES);
		const result = applyDrop(full, { kind: "session", sessionId: "new" }, "s3", "right");
		expect(result.refused).toBe(true);
		expect(result.root).toBe(full);
	});

	it("pane drag, centre → swap the two sessions", () => {
		const result = applyDrop(threePane(), { kind: "pane", sessionId: "a" }, "c", "center");
		expect(paneSessionIds(result.root)).toEqual(["c", "b", "a"]);
		expect(result.refused).toBeFalsy();
	});

	it("pane drag, edge → move, allowed even at the cap", () => {
		const full = chainOf(MAX_SPLIT_PANES);
		const result = applyDrop(full, { kind: "pane", sessionId: "s0" }, "s9", "down");
		expect(result.refused).toBeFalsy();
		expect(paneCount(result.root)).toBe(MAX_SPLIT_PANES);
	});

	it("a pane dropped on its own centre is a no-op", () => {
		const root = threePane();
		expect(applyDrop(root, { kind: "pane", sessionId: "b" }, "b", "center").root).toBe(root);
	});
});
