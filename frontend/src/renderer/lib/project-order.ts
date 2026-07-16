// Applies the user's persisted sidebar project order to the daemon's project
// list, and computes the new order after a drag-and-drop reorder. Pure: no
// mutation, no store/DOM access — so it is trivially unit-testable and shared
// by both the sidebar list and the ⌘1-9 project shortcut (single source of
// order). The persisted order lives in the ui-store (localStorage
// `ao.projects.order`), mirroring the `ao.projects.collapsed` pattern.

/**
 * Reorder `workspaces` by the saved id sequence `order`. Projects named in
 * `order` come first, in that sequence (ids no longer present are skipped);
 * any project NOT in the saved order — e.g. a project added after the order
 * was last saved — keeps its incoming (daemon-default) relative position and
 * is appended after the known ones. An empty saved order leaves the list as-is.
 */
export function orderWorkspaces<T extends { id: string }>(workspaces: readonly T[], order: readonly string[]): T[] {
	if (order.length === 0) return [...workspaces];
	const byId = new Map(workspaces.map((w) => [w.id, w]));
	const known: T[] = [];
	for (const id of order) {
		const w = byId.get(id);
		if (w) known.push(w);
	}
	const inOrder = new Set(order);
	const appended = workspaces.filter((w) => !inOrder.has(w.id));
	return [...known, ...appended];
}

/**
 * Given the CURRENT visible id sequence, produce the new sequence after
 * dropping `draggedId` on the `edge` (top/bottom half) of `targetId`. The
 * dragged id is removed first, then re-inserted relative to the target, so
 * dropping onto an adjacent neighbour lands where the pointer indicates.
 * Returns the sequence unchanged for a no-op (same id) or an unknown target.
 */
export function moveProject(
	orderedIds: readonly string[],
	draggedId: string,
	targetId: string,
	edge: "top" | "bottom",
): string[] {
	if (draggedId === targetId) return [...orderedIds];
	const without = orderedIds.filter((id) => id !== draggedId);
	const targetIndex = without.indexOf(targetId);
	if (targetIndex === -1) return [...orderedIds];
	const insertAt = edge === "top" ? targetIndex : targetIndex + 1;
	return [...without.slice(0, insertAt), draggedId, ...without.slice(insertAt)];
}
