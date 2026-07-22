// The scene layer: which prop a Proc's state puts under it.
//
// Scenes are a PROP LAYER, not fifteen drawings. The design fixes three
// composable slots — GROUND (desk · bed · crate · none), HELD and EMIT — and
// this PR ships the GROUND slot only, because ground is the load-bearing one:
//
//   A prop that is a PLACE anchors the Proc. Everything else may occasionally stroll.
//
// So "can this Proc walk?" is not a second table someone can get out of sync with
// the art — it is `groundFor(status) === "none"`. A working Proc is at a desk, and
// therefore physically cannot be found wandering. HELD/EMIT land with the full art PR.

import type { SessionStatus } from "../renderer/types/workspace";

/** The GROUND slot vocabulary. `none` means the Proc is standing on nothing but the floor band. */
export type Ground = "desk" | "bed" | "crate" | "none";

/** Every status the companion renders. Mirrors the 15 real `SessionStatus` values. */
export const ALL_COMPANION_STATUSES: SessionStatus[] = [
	"todo",
	"working",
	"pr_open",
	"draft",
	"ci_failed",
	"review_pending",
	"changes_requested",
	"approved",
	"mergeable",
	"merged",
	"needs_input",
	"no_signal",
	"idle",
	"terminated",
	"unknown",
];

// Only the states that genuinely denote a PLACE get a ground. `no_signal` is
// deliberately absent: continuing to draw a Proc typing at a desk for a session we
// have lost contact with is exactly the lie the bubble's TTL exists to prevent.
const GROUNDS: Partial<Record<SessionStatus, Ground>> = {
	working: "desk",
	ci_failed: "desk",
	idle: "bed",
	todo: "crate",
};

/** The GROUND prop for a status. */
export function groundFor(status: SessionStatus): Ground {
	return GROUNDS[status] ?? "none";
}

/**
 * True when the status puts the Proc at a place. Derived from {@link groundFor} —
 * never a parallel list — so the art and the locomotion rule cannot drift apart.
 */
export function isAnchored(status: SessionStatus): boolean {
	return groundFor(status) !== "none";
}
