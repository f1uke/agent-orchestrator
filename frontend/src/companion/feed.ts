// The contract between the overlay and whatever tells it what the sessions are
// doing. Deliberately tiny: one snapshot of (session, status) pairs, pushed.
//
// This PR ships one implementation (the mock in `mock-feed.ts`). The feed-wiring
// PR adds a second one over `GET /api/v1/activity/stream` and conforms to THIS
// interface — the overlay does not learn about SSE, reconnects or event decay.
//
// Two properties the real implementation must preserve:
//   1. Each push is the COMPLETE roster, not a delta. `syncActivities` reconciles
//      by session id: a session missing from a snapshot means its Proc goes away.
//      A delta protocol would leave a Proc on screen for a session that ended —
//      the same class of lie the feed's TTL rules exist to prevent.
//   2. `status` is the derived `SessionStatus`, which the daemon computes at read
//      time. The overlay never caches or infers it.
//
// The bubble's activity text (Bash `tool_input.description`) and its TTL are NOT
// here: nothing in this PR renders them, and the bubble PR will extend
// `CompanionActivity` with them rather than have the overlay guess at a shape.

import type { SessionStatus } from "../renderer/types/workspace";
import type { SessionKind } from "./live-roster";

/** One session, as much as the overlay needs to know about it. */
export type CompanionActivity = {
	sessionId: string;
	status: SessionStatus;
	/** The session's board name, shown under its Proc. Optional: no name, no chip. */
	name?: string;
	/** Which project it belongs to. Shown in the hover tooltip. */
	project?: string;
	/** The one coordinator per project is marked on its label. Absent = a worker. */
	kind?: SessionKind;
};

export interface CompanionFeed {
	/**
	 * Register for roster snapshots. The listener is called once immediately with
	 * the current roster (so the overlay never renders an empty first frame while
	 * waiting for a change), then on every update. Returns an unsubscribe.
	 */
	subscribe(listener: (activities: CompanionActivity[]) => void): () => void;
}
