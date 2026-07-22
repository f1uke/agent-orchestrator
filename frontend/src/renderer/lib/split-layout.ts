// The multi-terminal split layout: a binary tree of panes, one session per
// leaf, persisted per project (ui-store, localStorage "ao.split.layouts").
// Pure data + operations only — rendering lives in SplitTreeView, persistence
// in the ui-store. One session never appears in two leaves: every operation
// that introduces a session refuses when it is already on screen, which is
// what keeps two attach clients from ever fighting over one tmux session's
// grid (tmux sizes a session to its smallest attached client).

import type { WorkspaceSession } from "../types/workspace";

export type SplitDirection = "right" | "down";
export type SplitOrientation = "horizontal" | "vertical";
export type SplitLeaf = { kind: "leaf"; sessionId: string };
export type SplitBranch = {
	kind: "split";
	orientation: SplitOrientation;
	/** Share of the split's axis given to `first`, 0.05..0.95. */
	ratio: number;
	first: SplitNode;
	second: SplitNode;
};
export type SplitNode = SplitLeaf | SplitBranch;

/**
 * Hard pane cap per layout (user decision 2026-07-22, deliberately above the
 * 4 this codebase first proposed). It stays under Chromium's ~16 WebGL
 * contexts per page — the ceiling past which xterm panes silently fall back
 * to the DOM renderer — while width is governed separately: below the pane
 * floor the split region scrolls instead of crushing panes (SplitTreeView).
 */
export const MAX_SPLIT_PANES = 10;
/** Pane floor: ~50 cols × 12 rows at the default 12px terminal font, plus the toolbar. */
export const MIN_PANE_WIDTH = 380;
export const MIN_PANE_HEIGHT = 220;
/** The rrp separator is a 1px hairline (components/ui/resizable). */
export const SPLIT_HANDLE_SIZE = 1;

const MIN_RATIO = 0.05;
const MAX_RATIO = 0.95;

export function leaf(sessionId: string): SplitLeaf {
	return { kind: "leaf", sessionId };
}

function clampRatio(ratio: number): number {
	return Math.min(MAX_RATIO, Math.max(MIN_RATIO, ratio));
}

/** Leaf session ids in visual order (first/left/top before second/right/bottom). */
export function paneSessionIds(node: SplitNode): string[] {
	if (node.kind === "leaf") return [node.sessionId];
	return [...paneSessionIds(node.first), ...paneSessionIds(node.second)];
}

export function paneCount(node: SplitNode): number {
	return paneSessionIds(node).length;
}

export function containsSession(node: SplitNode, sessionId: string): boolean {
	if (node.kind === "leaf") return node.sessionId === sessionId;
	return containsSession(node.first, sessionId) || containsSession(node.second, sessionId);
}

/**
 * Replace the target leaf with a branch holding it and the new session:
 * "right" puts the new pane beside it (horizontal), "down" below (vertical).
 * Returns the tree unchanged when the target is missing, the session is
 * already on screen, or the cap is reached.
 */
export function splitPane(
	root: SplitNode,
	targetSessionId: string,
	direction: SplitDirection,
	newSessionId: string,
): SplitNode {
	if (containsSession(root, newSessionId)) return root;
	if (!containsSession(root, targetSessionId)) return root;
	if (paneCount(root) >= MAX_SPLIT_PANES) return root;
	const orientation: SplitOrientation = direction === "right" ? "horizontal" : "vertical";
	const replace = (node: SplitNode): SplitNode => {
		if (node.kind === "leaf") {
			if (node.sessionId !== targetSessionId) return node;
			return { kind: "split", orientation, ratio: 0.5, first: node, second: leaf(newSessionId) };
		}
		return { ...node, first: replace(node.first), second: replace(node.second) };
	};
	return replace(root);
}

export type AddPaneResult = { root: SplitNode; mode: "split" | "swapped" | "noop" };

/**
 * The navigation add: opening a session not yet on screen while the split view
 * is active shows it as a new pane, splitting the focused pane to the right.
 * At the cap it swaps into the focused pane instead (explicit user decision) —
 * the caller surfaces that swap, it must not be silent.
 */
export function addPane(root: SplitNode, focusedSessionId: string, newSessionId: string): AddPaneResult {
	if (containsSession(root, newSessionId)) return { root, mode: "noop" };
	const target = containsSession(root, focusedSessionId) ? focusedSessionId : (paneSessionIds(root).at(-1) as string);
	if (paneCount(root) >= MAX_SPLIT_PANES) {
		return { root: replaceSession(root, target, newSessionId), mode: "swapped" };
	}
	return { root: splitPane(root, target, "right", newSessionId), mode: "split" };
}

/**
 * Remove a pane from view, promoting its sibling. Returns null when the last
 * pane goes (the caller drops the layout and the view returns to single).
 * Pure view-state surgery: nothing here (or in any caller) touches session
 * lifecycle — the session keeps running, only its pane unmounts.
 */
export function removePane(root: SplitNode, sessionId: string): SplitNode | null {
	if (!containsSession(root, sessionId)) return root;
	const remove = (node: SplitNode): SplitNode | null => {
		if (node.kind === "leaf") return node.sessionId === sessionId ? null : node;
		const first = remove(node.first);
		const second = remove(node.second);
		if (first === null) return second;
		if (second === null) return first;
		if (first === node.first && second === node.second) return node;
		return { ...node, first, second };
	};
	return remove(root);
}

/**
 * Exchange two leaves' sessions in place — the centre-drop of a pane drag.
 * Structure and every divider ratio are untouched; only the two ids move, so
 * this is always allowed (the pane count cannot change). No-op onto itself or
 * when either session is absent.
 */
export function swapPanes(root: SplitNode, sessionA: string, sessionB: string): SplitNode {
	if (sessionA === sessionB) return root;
	if (!containsSession(root, sessionA) || !containsSession(root, sessionB)) return root;
	const swap = (node: SplitNode): SplitNode => {
		if (node.kind === "leaf") {
			if (node.sessionId === sessionA) return leaf(sessionB);
			if (node.sessionId === sessionB) return leaf(sessionA);
			return node;
		}
		return { ...node, first: swap(node.first), second: swap(node.second) };
	};
	return swap(root);
}

/**
 * Move an existing pane to a new position — the edge-drop of a pane drag.
 * Detach the dragged leaf (collapsing its now-single-child parent, the same
 * promotion `removePane` does), then re-split at the target in `direction`.
 * The pane COUNT is unchanged, so a move is allowed even at the cap. No-op
 * onto itself or when a session is missing. Reusing removePane+splitPane keeps
 * the collapse rules identical to the rest of the tree surgery.
 */
export function movePane(
	root: SplitNode,
	draggedSessionId: string,
	targetSessionId: string,
	direction: SplitDirection,
): SplitNode {
	if (draggedSessionId === targetSessionId) return root;
	if (!containsSession(root, draggedSessionId) || !containsSession(root, targetSessionId)) return root;
	const detached = removePane(root, draggedSessionId);
	// Removing one of >=2 leaves always leaves a tree; null would mean the
	// dragged pane was the only one, impossible given the target differs.
	if (detached === null) return root;
	// The target survived the detach (we removed a different session), and the
	// dragged id is gone, so splitPane's "already on screen" / cap guards both
	// pass — the net count returns to the original, never above it.
	return splitPane(detached, targetSessionId, direction, draggedSessionId);
}

/** Swap one session for another in place, preserving the split structure. */
export function replaceSession(root: SplitNode, oldSessionId: string, newSessionId: string): SplitNode {
	if (!containsSession(root, oldSessionId)) return root;
	const replace = (node: SplitNode): SplitNode => {
		if (node.kind === "leaf") return node.sessionId === oldSessionId ? leaf(newSessionId) : node;
		return { ...node, first: replace(node.first), second: replace(node.second) };
	};
	return replace(root);
}

/**
 * Drop leaves whose session is gone (layout restore: a saved layout may name
 * sessions that terminated or vanished since). Identity-preserving when
 * nothing changes, so callers can cheaply detect "no prune needed".
 */
export function pruneToSessions(root: SplitNode, alive: ReadonlySet<string>): SplitNode | null {
	if (root.kind === "leaf") return alive.has(root.sessionId) ? root : null;
	const first = pruneToSessions(root.first, alive);
	const second = pruneToSessions(root.second, alive);
	if (first === null) return second;
	if (second === null) return first;
	if (first === root.first && second === root.second) return root;
	return { ...root, first, second };
}

/**
 * Set a branch's ratio. The path addresses a node from the root: "" is the
 * root, then one "f"/"s" (first/second) per level — the same paths
 * SplitTreeView hands its resize handlers. A path that does not land on a
 * branch leaves the tree unchanged.
 */
export function setRatioAtPath(root: SplitNode, path: string, ratio: number): SplitNode {
	if (root.kind === "leaf") return root;
	if (path === "") return { ...root, ratio: clampRatio(ratio) };
	const step = path[0];
	if (step === "f") {
		const first = setRatioAtPath(root.first, path.slice(1), ratio);
		return first === root.first ? root : { ...root, first };
	}
	if (step === "s") {
		const second = setRatioAtPath(root.second, path.slice(1), ratio);
		return second === root.second ? root : { ...root, second };
	}
	return root;
}

/**
 * Minimum extent (px) the tree needs to show every pane at the floor size.
 * When the split region is smaller than this, the region scrolls rather than
 * crushing panes below usability (user decision: adding is permitted up to
 * the cap; how many are comfortably visible depends on the window).
 */
export function requiredExtent(node: SplitNode): { width: number; height: number } {
	if (node.kind === "leaf") return { width: MIN_PANE_WIDTH, height: MIN_PANE_HEIGHT };
	const first = requiredExtent(node.first);
	const second = requiredExtent(node.second);
	if (node.orientation === "horizontal") {
		return {
			width: first.width + second.width + SPLIT_HANDLE_SIZE,
			height: Math.max(first.height, second.height),
		};
	}
	return {
		width: Math.max(first.width, second.width),
		height: first.height + second.height + SPLIT_HANDLE_SIZE,
	};
}

// ---- persistence (localStorage "ao.split.layouts", versioned envelope) ----

const LAYOUTS_VERSION = 1;

export function serializeSplitLayouts(layouts: Record<string, SplitNode>): string {
	return JSON.stringify({ v: LAYOUTS_VERSION, layouts });
}

function sanitizeNode(value: unknown): SplitNode | null {
	if (typeof value !== "object" || value === null) return null;
	const node = value as Record<string, unknown>;
	if (node.kind === "leaf") {
		return typeof node.sessionId === "string" ? leaf(node.sessionId) : null;
	}
	if (node.kind === "split") {
		if (node.orientation !== "horizontal" && node.orientation !== "vertical") return null;
		if (typeof node.ratio !== "number" || !Number.isFinite(node.ratio)) return null;
		const first = sanitizeNode(node.first);
		const second = sanitizeNode(node.second);
		if (!first || !second) return null;
		return { kind: "split", orientation: node.orientation, ratio: clampRatio(node.ratio), first, second };
	}
	return null;
}

/**
 * Parse the persisted layouts map, dropping anything malformed (a bad project
 * entry never takes the healthy ones down with it).
 */
export function parseSplitLayouts(raw: string | null): Record<string, SplitNode> {
	if (!raw) return {};
	let parsed: unknown;
	try {
		parsed = JSON.parse(raw);
	} catch {
		return {};
	}
	if (typeof parsed !== "object" || parsed === null) return {};
	const envelope = parsed as { v?: unknown; layouts?: unknown };
	if (envelope.v !== LAYOUTS_VERSION || typeof envelope.layouts !== "object" || envelope.layouts === null) {
		return {};
	}
	const layouts: Record<string, SplitNode> = {};
	for (const [projectId, value] of Object.entries(envelope.layouts)) {
		const node = sanitizeNode(value);
		if (node) layouts[projectId] = node;
	}
	return layouts;
}

// ---- keyboard focus movement ----

export type PaneRect = { sessionId: string; rect: { left: number; top: number; width: number; height: number } };
export type FocusDirection = "left" | "right" | "up" | "down";

/**
 * The pane to focus when moving from `fromId` in a direction, from measured
 * pane rects (tree math cannot answer this: visual adjacency depends on
 * ratios). Candidates must lie beyond the source's centre on the direction
 * axis; the nearest wins, with cross-axis offset penalised so an aligned pane
 * beats a diagonal one. Null at an edge.
 */
export function nearestPaneInDirection(
	panes: readonly PaneRect[],
	fromId: string,
	direction: FocusDirection,
): string | null {
	const from = panes.find((p) => p.sessionId === fromId);
	if (!from) return null;
	const centre = (r: PaneRect["rect"]) => ({ x: r.left + r.width / 2, y: r.top + r.height / 2 });
	const fromCentre = centre(from.rect);
	let best: { sessionId: string; score: number } | null = null;
	for (const pane of panes) {
		if (pane.sessionId === fromId) continue;
		const c = centre(pane.rect);
		const dx = c.x - fromCentre.x;
		const dy = c.y - fromCentre.y;
		const forward = direction === "right" ? dx : direction === "left" ? -dx : direction === "down" ? dy : -dy;
		if (forward <= 0) continue;
		const sideways = direction === "left" || direction === "right" ? Math.abs(dy) : Math.abs(dx);
		const score = forward + sideways * 2;
		if (!best || score < best.score) best = { sessionId: pane.sessionId, score };
	}
	return best?.sessionId ?? null;
}

// ---- picker eligibility ----

/**
 * Sessions offered by the "add a pane" picker: this project's live sessions
 * (workers and the orchestrator, idle included), minus terminated ones,
 * unstarted todos (no terminal to show), and sessions already on screen — a
 * session on screen is simply never offered again (decision: one pane per
 * session, enforced structurally rather than checked at submit).
 */
export function eligibleSplitSessions(
	sessions: readonly WorkspaceSession[],
	root: SplitNode | null,
): WorkspaceSession[] {
	return sessions.filter(
		(s) => !s.isTerminated && s.status !== "terminated" && !s.isTodo && !(root !== null && containsSession(root, s.id)),
	);
}

// ---- drag-and-drop ----

/** What is being dragged onto a pane: a not-yet-shown sidebar session, or an existing pane. */
export type DragSource = { kind: "session"; sessionId: string } | { kind: "pane"; sessionId: string };
/** The drop target region within a pane. `center` is a swap; `right`/`down` split or move. */
export type DropZone = "center" | "right" | "down";

// Pane-drag geometry: a right edge strip and a bottom edge strip carve out the
// move zones; the rest is the central swap zone. The right strip owns the
// bottom-right corner so the two strips never overlap (a point is in exactly
// one zone). A sidebar session has no swap, so its whole pane is one diagonal
// split into right (upper-right) and down (lower-left).
const EDGE_STRIP = 0.3;

/**
 * Resolve which drop zone a pointer at (relX, relY) — pane-relative fractions
 * in [0,1] — falls into. Pure so the overlay highlight and the drop handler
 * agree by construction and the geometry is unit-tested.
 */
export function resolveDropZone(relX: number, relY: number, kind: DragSource["kind"]): DropZone {
	if (kind === "session") {
		// Distance to the right edge vs the bottom edge; the nearer edge wins.
		// Ties (incl. the exact centre) resolve to right — sidebar has no centre.
		return 1 - relX <= 1 - relY ? "right" : "down";
	}
	if (relX >= 1 - EDGE_STRIP) return "right"; // right strip, full height (owns the corner)
	if (relY >= 1 - EDGE_STRIP) return "down"; // bottom strip, left of the right strip
	return "center";
}

export type DropResult = { root: SplitNode; refused?: boolean };

/**
 * Apply a drop onto `targetSessionId`'s pane. Dispatches by source + zone:
 * a sidebar session splits the target (right/down; centre never occurs for a
 * session); an existing pane swaps on centre or moves on an edge. A sidebar
 * split that the cap blocks is REFUSED (same signal the click path shows), not
 * silently dropped; a pane move/swap never changes the count, so it is always
 * allowed. Identity-stable when nothing changes.
 */
export function applyDrop(root: SplitNode, source: DragSource, targetSessionId: string, zone: DropZone): DropResult {
	if (source.kind === "session") {
		const direction: SplitDirection = zone === "down" ? "down" : "right";
		const next = splitPane(root, targetSessionId, direction, source.sessionId);
		// splitPane returns the same tree when refused; at the cap that is a
		// refusal the caller must surface, otherwise it is just a no-op.
		if (next === root && paneCount(root) >= MAX_SPLIT_PANES && !containsSession(root, source.sessionId)) {
			return { root, refused: true };
		}
		return { root: next };
	}
	if (zone === "center") {
		return { root: swapPanes(root, source.sessionId, targetSessionId) };
	}
	return { root: movePane(root, source.sessionId, targetSessionId, zone === "down" ? "down" : "right") };
}
