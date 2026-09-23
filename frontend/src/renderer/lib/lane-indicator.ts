import { Archive, Check, Circle, CircleDot, CircleDashed, Contrast, type LucideIcon } from "lucide-react";
import type { AttentionZone } from "../types/workspace";

// The four board lanes, each owning one hue in a 4-color semantic system
// (design handoff Board.dc.html). The lane now shows itself ONLY in the column
// header — its shape glyph and label — never as a painted edge or wash; the
// coloured top rails, hue tints and card left-accents that used to repeat it
// five times per column are retired. NEEDS YOU is coral — moved off amber so it
// no longer collides with WORKING.
//
// Per-CARD status shapes live in lib/status-glyph: a lane is coarser than a
// status (NEEDS YOU alone holds four), so the lane glyph identifies the column
// while each card draws its own.
export type LaneKey = "todo" | "working" | "action" | "pending" | "merge";

/**
 * Every lane the board draws: the five live lanes plus Done, where merged and
 * terminated sessions go. Done is a board lane only - the sidebar drops finished
 * sessions, so {@link LaneKey} and {@link LANE_ORDER} stay the live five.
 */
export type BoardLaneKey = LaneKey | "done";

export type LaneConfig<K extends BoardLaneKey = LaneKey> = {
	key: K;
	/** Board column header label. */
	label: string;
	/** The lane's hue: column header label + glyph, and every card's status glyph. */
	dotVar: string;
	/** Sidebar / empty-lane glyph shape. */
	Icon: LucideIcon;
	/** Render the glyph filled (the WORKING ● solid dot). */
	filled: boolean;
	/** Empty-lane placeholder message. */
	emptyText: string;
};

export const LANES: Record<LaneKey, LaneConfig> = {
	todo: {
		key: "todo",
		label: "Todo",
		dotVar: "var(--lane-todo-bright)",
		Icon: CircleDashed,
		filled: false,
		emptyText: "Nothing queued",
	},
	working: {
		key: "working",
		label: "Working",
		dotVar: "var(--lane-working-bright)",
		Icon: Circle,
		filled: true,
		emptyText: "Nothing in progress",
	},
	action: {
		key: "action",
		label: "Needs you",
		dotVar: "var(--lane-needs-bright)",
		Icon: CircleDot,
		filled: false,
		emptyText: "Nothing needs you",
	},
	pending: {
		key: "pending",
		label: "In review",
		dotVar: "var(--lane-review-bright)",
		Icon: Contrast,
		filled: false,
		emptyText: "Nothing in review",
	},
	merge: {
		key: "merge",
		label: "Ready to merge",
		dotVar: "var(--lane-merge-bright)",
		Icon: Check,
		filled: false,
		emptyText: "Nothing ready to merge",
	},
};

// Finished work: merged and terminated sessions, newest ending first. It takes
// no hue of its own - the four-hue system is for work in flight, and an archive
// wearing one would read as a fifth state of work. Muted rather than passive, so
// its header still clears the contrast floor beside the coloured ones.
export const DONE_LANE: LaneConfig<"done"> = {
	key: "done",
	label: "Done",
	dotVar: "var(--fg-muted)",
	Icon: Archive,
	filled: false,
	emptyText: "Nothing finished yet",
};

// Left→right board order and, identically, the sidebar's sort order (the design
// sorts sidebar sessions by state in the same flow as the lanes).
export const LANE_ORDER: LaneKey[] = ["todo", "working", "action", "pending", "merge"];

// Maps a derived attention zone to its live lane. "done" has no live lane
// (terminated / merged sessions sit in the board's Done lane and leave the
// sidebar), so it falls back to the review lane for any defensive caller.
export function laneForZone(zone: AttentionZone): LaneConfig {
	return zone === "done" ? LANES.pending : LANES[zone];
}
