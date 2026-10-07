// Pure helpers for a task's own worktree of the mobile scripts store. On a
// project with mobileScripts each workspace writes scripts into its own git
// worktree of the store and publishes them with `ao scripts publish`; these
// helpers decide when the board says that some of that work has not reached
// the store, and in what words.

import type { components } from "../../api/schema";

export type SessionScriptsStore = components["schemas"]["SessionScriptsStore"];
export type ScriptsStoreHold = NonNullable<SessionScriptsStore["heldReason"]>;

/** Why a teardown kept the worktree, in the words a person would use. */
export const HOLD_WORDS: Record<ScriptsStoreHold, string> = {
	uncommitted: "it holds files nobody committed",
	publish_conflict: "its commits conflict with the store",
	store_dirty_overlap: "the store's main checkout has unsaved edits in the same files",
	store_off_base: "the store's main checkout is on another branch",
	publish_failed: "the publish failed",
};

/**
 * Whether the board should say so. A working agent is expected to have
 * uncommitted scripts mid-task, so counts alone show only once it stops; a
 * worktree a teardown kept always shows, because the card cannot move to Done
 * until somebody deals with it.
 */
export function showsUnpublishedScripts(store: SessionScriptsStore | undefined, status: string): boolean {
	if (!store) return false;
	if (store.heldReason) return true;
	return status !== "working" && (store.uncommitted > 0 || store.unpublished > 0);
}

/** The chip's tooltip: what is not in the store yet, file by file, and the fix. */
export function unpublishedScriptsTitle(store: SessionScriptsStore): string {
	const parts: string[] = [];
	if (store.uncommitted > 0) parts.push(`${store.uncommitted} uncommitted file${store.uncommitted === 1 ? "" : "s"}`);
	if (store.unpublished > 0) parts.push(`${store.unpublished} unpublished commit${store.unpublished === 1 ? "" : "s"}`);
	const lines = [
		parts.length > 0
			? `This task's scripts store worktree holds ${parts.join(" and ")} the store does not have.`
			: "This task's scripts store worktree holds work the store does not have.",
	];
	if (store.heldReason) lines.push(`Kept when the session ended: ${HOLD_WORDS[store.heldReason]}.`);
	lines.push(...store.files.map((file) => `  ${file}`));
	lines.push("Commit them in $AO_SCRIPTS_STORE, then run `ao scripts publish` in the session.");
	return lines.join("\n");
}
