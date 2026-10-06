// Pure helpers for the card's subagent strip: a worker's child worktrees (the
// subagents it ran with `isolation: "worktree"`) and what became of each one's
// work. AO cuts each child from the worker's branch, merges it back when the
// subagent stops, and keeps it on its own branch when that is not possible.
//
// The strip counts children by state, leading with what needs a person (a
// conflict, a merge held behind the worker's own edits, work kept on a branch
// after the worker ended), then what is still running, then what merged, then
// what finished without changes.

import type { components } from "../../api/schema";

export type SessionChild = components["schemas"]["SessionChild"];
export type ChildState = SessionChild["state"];

export type ChildMeta = {
	/** The state in the words a person would use. */
	word: string;
	/** CSS colour of the chip's glyph and label. */
	tone: string;
	/** True when a person has to act for this child's work to land. */
	attention: boolean;
};

export const CHILD_META: Record<ChildState, ChildMeta> = {
	running: { word: "running", tone: "var(--lane-working-bright)", attention: false },
	merging: { word: "merging", tone: "var(--lane-working-bright)", attention: false },
	held: { word: "merge held", tone: "var(--lane-needs-bright)", attention: true },
	conflict: { word: "conflict", tone: "var(--lane-needs-bright)", attention: true },
	preserved: { word: "kept on branch", tone: "var(--lane-needs-bright)", attention: true },
	merged: { word: "merged", tone: "var(--lane-merge-bright)", attention: false },
	removed: { word: "no changes", tone: "var(--fg-passive)", attention: false },
};

const ORDER: Record<ChildState, number> = {
	conflict: 0,
	held: 1,
	preserved: 2,
	merging: 3,
	running: 4,
	merged: 5,
	removed: 6,
};

/** What a child is called on the card: what the worker said it was for. */
export function childLabel(child: SessionChild): string {
	return child.description?.trim() || child.agentType?.trim() || child.agentId;
}

/** The tooltip: label, state, and whatever a person needs to act on it. */
export function childTooltip(child: SessionChild): string {
	const meta = CHILD_META[child.state];
	const parts = [`${childLabel(child)} — ${meta.word}`];
	if (child.commits > 0) {
		parts.push(
			`${child.commits} commit${child.commits === 1 ? "" : "s"}, ${child.filesChanged} file${child.filesChanged === 1 ? "" : "s"}`,
		);
	}
	if (child.detail) parts.push(child.detail);
	if (child.state === "conflict" || child.state === "held" || child.state === "preserved") {
		parts.push(`branch ${child.branch}`);
	}
	return parts.join(" · ");
}

/** One state's worth of children: a single pip on the strip. */
export type ChildGroup = {
	state: ChildState;
	children: SessionChild[];
};

/**
 * The strip's pips, most urgent state first. One pip per state keeps the strip
 * on one line at the board's real column width however many subagents a worker
 * ran; who they are lives in each pip's tooltip.
 */
export function childGroups(children: SessionChild[]): ChildGroup[] {
	const byState = new Map<ChildState, SessionChild[]>();
	for (const child of children) {
		const list = byState.get(child.state) ?? [];
		list.push(child);
		byState.set(child.state, list);
	}
	return [...byState.entries()]
		.sort(([a], [b]) => ORDER[a] - ORDER[b])
		.map(([state, list]) => ({
			state,
			children: [...list].sort((a, b) => a.createdAt.localeCompare(b.createdAt)),
		}));
}
