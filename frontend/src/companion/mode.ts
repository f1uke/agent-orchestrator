// The four behaviour modes, and the one function that decides which a status gets.
//
// `anchor` is NOT listed here — it is read off the scene's ground (see scene.ts),
// so a state that gained or lost a place in the art cannot end up with a
// contradictory locomotion rule. Only the two exceptions to "ground decides" are
// enumerated: the states with nothing live to show, and the one state that is
// about the human rather than about the work.

import type { SessionStatus } from "../renderer/types/workspace";
import { isAnchored } from "./scene";

export type CompanionMode =
	/** At its place (bed / crate). Cannot walk. */
	| "anchor"
	/** Free to take an occasional short stroll. */
	| "amble"
	/** Walks to the front ONCE and then stands facing the human. */
	| "summon"
	/** Stands where it is. The default, and what an unknown state falls back to. */
	| "still";

/**
 * Only a PLACE keeps a Proc still.
 *
 * `no_signal`, `merged`, `terminated` and `unknown` used to be held still as well,
 * on the argument that motion asserts liveness we do not have. The human overruled
 * it (2026-07-23): everything walks except the two states that ARE somewhere — in a
 * bed, or still in its crate. The truthfulness the feed guarantees is in what a Proc
 * SAYS, which is unchanged; a pet strolling is ambience, and a quiet session's Proc
 * still shows the quiet scene, the unplugged cord and no bubble at all.
 *
 * `still` remains in the vocabulary because reduced motion and parking use it, and
 * because a state with no place and no motion is a coherent thing to want later.
 */
export function modeFor(status: SessionStatus): CompanionMode {
	if (status === "needs_input") return "summon";
	return isAnchored(status) ? "anchor" : "amble";
}
