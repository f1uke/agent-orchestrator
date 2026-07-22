// The scene layer: what a Proc's state puts around it.
//
// Scenes are a PROP LAYER, not fifteen drawings. Four composable slots make the 15
// states, so a sixteenth state is a row here rather than a new picture:
//
//   GROUND  desk · bed · crate · none        — where it is
//   HELD    page ×4 surfaces · 2 signs · none — what it is holding
//   EMIT    zzz · sparks · confetti · quiet · none — what is coming off it
//   CORD    attached · streaming · tugging · sparking · coiled · unplugged — the link
//
// Two rules keep the slots from saying the same thing twice:
//
//   1. A prop that is a PLACE anchors the Proc. Everything else may occasionally
//      stroll. So "can this Proc walk?" is not a second table someone can get out
//      of sync with the art — it is `groundFor(status) === "none"`, and a working
//      Proc is at a desk and therefore physically cannot be found wandering.
//   2. The cord leaves RIGHT and held props sit LEFT. The cord is the LINK
//      (attached / streaming / tugging / sparking / unplugged); props are the TASK.
//      Kept physically apart so they cannot double-encode.

import type { SessionStatus } from "../renderer/types/workspace";

/** The GROUND slot vocabulary. `none` means the Proc is standing on nothing but the floor band. */
export type Ground = "desk" | "bed" | "crate" | "none";

/**
 * The HELD slot: one page shape across four surfaces, plus two signs.
 *
 * The design specifies "one page shape ×4 surfaces, 2 signs, none". A fifth
 * surface (`page-clock`) is added deliberately: without it `review_pending` and
 * `pr_open` draw identically, and two states that look the same are two states the
 * overlay cannot tell you apart — the exact complaint this PR exists to fix.
 */
export type Held =
	"page-blank" | "page-lines" | "page-clock" | "page-check" | "page-cross" | "sign-question" | "sign-merge" | "none";

/** The EMIT slot: what is coming off the Proc. */
export type Emit = "zzz" | "sparks" | "confetti" | "quiet" | "none";

/** What the cord — the link to the session — is doing. */
export type Cord = "attached" | "streaming" | "tugging" | "sparking" | "coiled" | "unplugged";

export type Scene = {
	ground: Ground;
	held: Held;
	emit: Emit;
	cord: Cord;
};

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

// The 15 states as slot combinations. GROUND is not repeated here — it is read
// from GROUNDS above, so the scene and the anchoring rule are literally the same
// fact. A scene table that carried its own copy of the ground could disagree with
// the locomotion rule, and a working Proc could be drawn at a desk and still be
// allowed to wander off it.
const SCENES: Record<SessionStatus, Omit<Scene, "ground">> = {
	// Prepared, not started: a crate of work, cord neatly coiled, nothing coming off it.
	todo: { held: "none", emit: "none", cord: "coiled" },
	// At the desk with a written page; the cord carries data while the agent runs.
	working: { held: "page-lines", emit: "none", cord: "streaming" },
	// At the desk, but the run failed: sparks off the cord instead of data.
	ci_failed: { held: "none", emit: "sparks", cord: "sparking" },
	// Asleep in bed. The one state that is supposed to look like nothing happening.
	idle: { held: "none", emit: "zzz", cord: "coiled" },
	// A PR is up and waiting on the world.
	pr_open: { held: "page-lines", emit: "none", cord: "attached" },
	// A draft: the page exists but nothing is written on it yet.
	draft: { held: "page-blank", emit: "none", cord: "attached" },
	// Waiting on a reviewer — the page has a clock on it, so it is not just "a PR".
	review_pending: { held: "page-clock", emit: "none", cord: "attached" },
	// A reviewer wrote back asking for changes.
	changes_requested: { held: "page-cross", emit: "none", cord: "attached" },
	// Signed off.
	approved: { held: "page-check", emit: "none", cord: "attached" },
	// One click from done, so it holds up a sign rather than a page: this one is
	// addressed to YOU, and signs are rationed to the states that are.
	mergeable: { held: "sign-merge", emit: "none", cord: "attached" },
	// Done. Confetti, and the cord comes out — the session is over.
	merged: { held: "none", emit: "confetti", cord: "unplugged" },
	// The other state addressed to you: it comes to the front and asks.
	needs_input: { held: "sign-question", emit: "none", cord: "tugging" },
	// We have lost contact. Deliberately the most inert scene in the set: no ground,
	// no prop, an unplugged cord and a few muted dots. Nothing here claims liveness.
	no_signal: { held: "none", emit: "quiet", cord: "unplugged" },
	// Over, without a merge.
	terminated: { held: "none", emit: "none", cord: "unplugged" },
	// We could not read the status at all, so we draw the fewest claims possible —
	// not even the quiet dots, which would be claiming that nothing is coming.
	unknown: { held: "none", emit: "none", cord: "attached" },
};

/** The full scene for a status: the four slots, with GROUND read from the one table. */
export function sceneFor(status: SessionStatus): Scene {
	return { ground: groundFor(status), ...SCENES[status] };
}

// Which EMIT/CORD values actually move. This is what makes the engine's ~8
// animating backstop real rather than theoretical: with the full art, most of the
// animation on screen is scenes, not walkers.
const ANIMATED_EMIT = new Set<Emit>(["zzz", "sparks", "confetti"]);
const ANIMATED_CORD = new Set<Cord>(["streaming", "tugging", "sparking"]);

/** True when a status's scene has something moving in it. */
export function sceneAnimates(status: SessionStatus): boolean {
	const scene = sceneFor(status);
	return ANIMATED_EMIT.has(scene.emit) || ANIMATED_CORD.has(scene.cord);
}
