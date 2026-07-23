import type { SessionStatus } from "../renderer/types/workspace";
import type { components } from "../api/schema";
import type { CompanionActivity } from "./feed";

// Which Procs exist, and what state they are in — read from the SESSIONS API, not
// from the activity feed.
//
// The split is deliberate and comes from the feed's contract: the feed is data-only
// and carries no status, because a status is a DERIVED fact the daemon recomputes
// at read time and an event stream would inevitably serve a stale copy of it. So
// scenes come from here, and only the bubble's words come from the stream.

type SessionView = components["schemas"]["ControllersSessionView"];

/** Just the fields the overlay reads. Kept narrow so a session shape change is loud. */
export type LiveSession = {
	id: string;
	name: string;
	projectName?: string;
	/** Which job this session holds. The one coordinator is marked on its label. */
	kind: SessionKind;
	status: SessionView["status"];
	statusReason?: SessionView["statusReason"];
	isTerminated: boolean;
};

/** Only the distinction the overlay draws. Everything that is not the coordinator works. */
export type SessionKind = "orchestrator" | "worker";

/**
 * True only when the agent itself raised a prompt and is genuinely blocked on the
 * human.
 *
 * `waiting_input` is a real permission prompt. `idle_aged` and `active_stale` are
 * AO's timeout GUESSES — it has been quiet a while, so we suspect it wants us. All
 * three can land on `needs_input`, and only the first has earned the right to walk
 * a Proc to the front and ask for attention. Crying wolf on a guess is how an
 * ambient thing becomes something you switch off.
 */
export function isGenuinelyWaiting(session: LiveSession): boolean {
	return session.status === "needs_input" && session.statusReason === "waiting_input";
}

// A session we only INFERRED was waiting is shown as what we actually know: we have
// not heard from it. That is a real, calm scene — no summon, no badge, no alarm.
function overlayStatus(session: LiveSession): SessionStatus {
	if (session.status === "needs_input" && !isGenuinelyWaiting(session)) return "no_signal";
	return session.status;
}

/**
 * The live roster, as the behaviour engine's `CompanionActivity` list.
 *
 * EVERY live session, including the ones the desktop will turn out to have no room to
 * draw. Trimming it to what fits used to happen here, and that was the ghost-portal bug:
 * downstream, "not in this list" is how a session's END is recognised, so a session held
 * back by the cap was read as a session that had finished and was seen out through a
 * portal. How many Procs fit is a question about the BAND, and it is answered where the
 * band is — see `MAX_PETS` and `bandMembers` in `behaviour.ts`.
 */
export function sessionsToActivities(sessions: LiveSession[]): CompanionActivity[] {
	return sessions
		.filter((session) => !session.isTerminated)
		.map((session) => ({
			sessionId: session.id,
			status: overlayStatus(session),
			name: session.name,
			project: session.projectName ?? "",
			kind: session.kind,
		}));
}
