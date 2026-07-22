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
	/** At its prop (desk / bed / crate). Cannot walk. */
	| "anchor"
	/** Free to take an occasional short stroll. */
	| "amble"
	/** Walks to the front ONCE and then stands facing the human. */
	| "summon"
	/** Stands where it is. The default, and what an unknown state falls back to. */
	| "still";

// States with no live truth behind them. A Proc that has gone quiet, merged or
// terminated must not perform: motion would assert liveness we do not have.
// `unknown` joins them for the same reason — it is the parse failure of a status.
const STILL: ReadonlySet<SessionStatus> = new Set<SessionStatus>(["no_signal", "merged", "terminated", "unknown"]);

export function modeFor(status: SessionStatus): CompanionMode {
	if (STILL.has(status)) return "still";
	if (status === "needs_input") return "summon";
	return isAnchored(status) ? "anchor" : "amble";
}
