import { Check, Circle, CircleDot, Contrast, type LucideIcon } from "lucide-react";
import type { AttentionZone } from "../types/workspace";

// The four board lanes, each owning one hue in a 4-color semantic system
// (design handoff Board.dc.html). A lane is identified across the board (column
// top border, tint, header dot/label, count badge, card accent) and the sidebar
// (a distinct glyph shape) by the same hue, so a session reads the same in both
// places. NEEDS YOU is coral — moved off amber so it no longer collides with
// WORKING. The glyph shapes (filled dot ● / ring ◎ / half ◐ / check ✓) make the
// sidebar scannable by shape as well as hue, using lucide equivalents of the
// design's unicode glyphs.
export type LaneKey = "working" | "action" | "pending" | "merge";

export type LaneConfig = {
	key: LaneKey;
	/** Board column header label. */
	label: string;
	/** Base hue: column top border, background tint, count badge. */
	hueVar: string;
	/** Brighter variant: status dot, header label, card accent, sidebar glyph. */
	dotVar: string;
	/** Sidebar / empty-lane glyph shape. */
	Icon: LucideIcon;
	/** Render the glyph filled (the WORKING ● solid dot). */
	filled: boolean;
	/** Empty-lane placeholder message. */
	emptyText: string;
};

export const LANES: Record<LaneKey, LaneConfig> = {
	working: {
		key: "working",
		label: "Working",
		hueVar: "var(--lane-working)",
		dotVar: "var(--lane-working-bright)",
		Icon: Circle,
		filled: true,
		emptyText: "Nothing in progress",
	},
	action: {
		key: "action",
		label: "Needs you",
		hueVar: "var(--lane-needs)",
		dotVar: "var(--lane-needs-bright)",
		Icon: CircleDot,
		filled: false,
		emptyText: "Nothing needs you",
	},
	pending: {
		key: "pending",
		label: "In review",
		hueVar: "var(--lane-review)",
		dotVar: "var(--lane-review-bright)",
		Icon: Contrast,
		filled: false,
		emptyText: "Nothing in review",
	},
	merge: {
		key: "merge",
		label: "Ready to merge",
		hueVar: "var(--lane-merge)",
		dotVar: "var(--lane-merge-bright)",
		Icon: Check,
		filled: false,
		emptyText: "Nothing ready to merge",
	},
};

// Left→right board order and, identically, the sidebar's sort order (the design
// sorts sidebar sessions by state in the same flow as the lanes).
export const LANE_ORDER: LaneKey[] = ["working", "action", "pending", "merge"];

// Maps a derived attention zone to its lane. "done" is not a lane (terminated /
// merged sessions live in the board's Done bar and leave the sidebar), so it
// falls back to the review lane for any defensive caller.
export function laneForZone(zone: AttentionZone): LaneConfig {
	return zone === "done" ? LANES.pending : LANES[zone];
}
