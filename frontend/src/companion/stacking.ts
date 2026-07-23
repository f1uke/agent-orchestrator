import type { Pet } from "./behaviour";

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

/** Highest wins. Ordered by how much the human is currently looking at it. */
export const STACK_LAYERS = {
	/** Going about its business. */
	resting: 0,
	/** Answering a roll-call: the huddle is the scene, so it stands over the idle band. */
	rallying: 5,
	/** Mid-conversation: a staged scene, and the pair belong in front of the crowd. */
	meeting: 10,
	/** In the human's hand. Nothing outranks the thing being dragged. */
	held: 20,
} as const;

export function stackOrder(pet: Pet): number {
	if (pet.motion.kind === "held") return STACK_BASE + STACK_LAYERS.held;
	if (pet.meeting) return STACK_BASE + STACK_LAYERS.meeting;
	if (pet.rally) return STACK_BASE + STACK_LAYERS.rallying;
	return STACK_BASE + STACK_LAYERS.resting;
}
