// Pure helpers for the card's subagent strip: a worker's child worktrees (the
// subagents it ran with `isolation: "worktree"`) and what became of each one's
// work. AO cuts each child from the worker's branch, merges it back when the
// subagent stops, and keeps it on its own branch when that is not possible.
//
// The strip leads with what needs a person (a held merge, a conflict, work kept
// on a branch after the worker ended), then what is still running, then what
// merged. A child that finished without changes is counted, never drawn: it
// left nothing behind to look at.

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

export type ChildStripModel = {
	/** Children drawn as chips, most urgent first. */
	shown: SessionChild[];
	/** Children with something to show that did not fit. */
	overflow: number;
	/** Children that finished without changes. */
	empty: number;
};

export function childStripModel(children: SessionChild[], maxChips = 3): ChildStripModel {
	const visible = children
		.filter((child) => child.state !== "removed")
		.sort((a, b) => ORDER[a.state] - ORDER[b.state] || a.createdAt.localeCompare(b.createdAt));
	return {
		shown: visible.slice(0, maxChips),
		overflow: Math.max(0, visible.length - maxChips),
		empty: children.length - visible.length,
	};
}
