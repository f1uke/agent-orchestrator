import type { SessionStatus } from "../renderer/types/workspace";
import type { components } from "../api/schema";
import type { CompanionActivity } from "./feed";
import { modeFor } from "./mode";

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
 * More Procs than this and the band stops being readable — they crowd, the names
 * collide, and the thing stops being glanceable, which is its only job. Sessions
 * beyond the cap are dropped from the OVERLAY only; nothing else in AO changes.
 */
export const MAX_PETS = 12;

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

// When the cap bites, keep the ones that want something from you first, then the
// ones doing something, then the rest. Dropping the pet that is asking for help
// would be the worst possible thing to drop.
function attentionRank(status: SessionStatus): number {
	const mode = modeFor(status);
	if (mode === "summon") return 0;
	if (mode === "anchor") return 1;
	if (mode === "amble") return 2;
	return 3;
}

/** The live roster, as the behaviour engine's `CompanionActivity` list. */
export function sessionsToActivities(sessions: LiveSession[]): CompanionActivity[] {
	const live = sessions
		.filter((session) => !session.isTerminated)
		.map((session) => ({ session, status: overlayStatus(session) }));

	const kept =
		live.length <= MAX_PETS
			? live
			: [...live].sort((a, b) => attentionRank(a.status) - attentionRank(b.status)).slice(0, MAX_PETS);

	return kept.map(({ session, status }) => ({
		sessionId: session.id,
		status,
		name: session.name,
		project: session.projectName ?? "",
		kind: session.kind,
	}));
}
