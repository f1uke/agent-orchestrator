import { RALLY_MAX_DEPTH, type Pet } from "./behaviour";

// Which Proc paints in front of which.
//
// Only CHARACTERS are ordered here. What they are saying lives in its own layer
// above the whole cast (see CompanionStage) — it had to, because every Proc
// carries a `transform` and a transform makes an element a stacking context, so a
// bubble drawn inside its own Proc could never rise above the Proc next door
// however its z-index was set.
//
// One ordered list rather than a rule per case, so two Procs can never both claim
// the front and the tie is broken by which claim is stronger.

export const STACK_BASE = 1;

/**
 * Highest wins. Ordered by how much the human is currently looking at it.
 *
 * Spaced a hundred apart rather than packed, because one of them — the roll-call —
 * is not a single layer but a ladder: the gathered photo overlaps its Procs on
 * purpose, so each carries its own depth within the band its layer opens. The gap is
 * what stops that ladder ever climbing into the layer above it.
 */
export const STACK_LAYERS = {
	/** Going about its business. */
	resting: 0,
	/** Answering a roll-call: the huddle is the scene, so it stands over the idle band. */
	rallying: 100,
	/** Mid-conversation: a staged scene, and the pair belong in front of the crowd. */
	meeting: 200,
	/** In the human's hand. Nothing outranks the thing being dragged. */
	held: 300,
} as const;

export function stackOrder(pet: Pet): number {
	if (pet.motion.kind === "held") return STACK_BASE + STACK_LAYERS.held;
	if (pet.meeting) return STACK_BASE + STACK_LAYERS.meeting;
	// The photo's own front-to-back order, decided by the engine when the row was laid
	// out. Bounded by RALLY_MAX_DEPTH, which is well under the gap to the next layer.
	if (pet.rally) return STACK_BASE + STACK_LAYERS.rallying + Math.min(pet.rally.depth, RALLY_MAX_DEPTH - 1);
	return STACK_BASE + STACK_LAYERS.resting;
}
