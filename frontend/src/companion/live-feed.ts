import { applyEvent, emptySlots, type ActivityFrame, type ActivitySlots } from "./activity-decay";
import { composeBubble, type ComposedBubble } from "./bubble-compose";
import type { CompanionActivity, CompanionFeed } from "./feed";
import { sessionsToActivities, type LiveSession } from "./live-roster";

// The real feed: which Procs exist comes from the sessions API, what they SAY comes
// from the activity stream. Two sources, because they answer different questions
// and have different truth models — a derived status must be read fresh, while an
// observed action is a fact with an expiry.
//
// It implements the same `CompanionFeed` the mock does, so the overlay is unchanged
// by the swap and the mock path stays usable in tests.

/** How often the roster is re-read. Statuses are derived, so they must be pulled. */
export const ROSTER_POLL_MS = 4_000;
/** Backoff before re-opening a dropped stream. Long enough not to hammer a daemon
 * that is restarting, short enough that a Proc is not mute for noticeably long. */
export const STREAM_RETRY_MS = 3_000;

export type LiveFeedDeps = {
	fetchSessions: () => Promise<LiveSession[]>;
	/** Opens the SSE. Returns a closer. */
	openStream: (onFrame: (frame: unknown) => void, onError: () => void) => () => void;
	now: () => number;
};

export type LiveFeed = CompanionFeed & {
	/** What this session's Proc may honestly say right now, or null for silence. */
	bubbleFor: (sessionId: string) => ComposedBubble | null;
	/** Re-read the roster immediately. */
	refreshNow: () => Promise<void>;
};

function isFrame(value: unknown): value is ActivityFrame {
	if (!value || typeof value !== "object") return false;
	const frame = value as Partial<ActivityFrame>;
	return typeof frame.sessionId === "string" && typeof frame.at === "string" && typeof frame.kind === "string";
}

export function createLiveFeed(deps: LiveFeedDeps): LiveFeed {
	const listeners = new Set<(activities: CompanionActivity[]) => void>();
	const slots = new Map<string, ActivitySlots>();
	// The last roster that actually arrived. A failed poll — a restarting daemon —
	// keeps the Procs on screen rather than emptying the desktop and refilling it.
	let roster: CompanionActivity[] = [];

	let pollTimer: ReturnType<typeof setInterval> | null = null;
	let retryTimer: ReturnType<typeof setTimeout> | null = null;
	let closeStream: (() => void) | null = null;
	let running = false;

	const publish = () => {
		for (const listener of listeners) listener(roster);
	};

	const refreshNow = async () => {
		try {
			roster = sessionsToActivities(await deps.fetchSessions());
			publish();
		} catch {
			// Keep the last good roster. A poll that failed tells us nothing about the
			// sessions, only about the connection.
		}
	};

	const openStream = () => {
		if (!running) return;
		closeStream = deps.openStream(
			(value) => {
				if (!isFrame(value)) return;
				slots.set(value.sessionId, applyEvent(slots.get(value.sessionId) ?? emptySlots(), value));
			},
			() => {
				closeStream?.();
				closeStream = null;
				if (!running) return;
				retryTimer = setTimeout(() => {
					retryTimer = null;
					openStream();
				}, STREAM_RETRY_MS);
			},
		);
	};

	const start = () => {
		if (running) return;
		running = true;
		void refreshNow();
		pollTimer = setInterval(() => void refreshNow(), ROSTER_POLL_MS);
		openStream();
	};

	const stop = () => {
		running = false;
		if (pollTimer) clearInterval(pollTimer);
		if (retryTimer) clearTimeout(retryTimer);
		pollTimer = null;
		retryTimer = null;
		closeStream?.();
		closeStream = null;
	};

	return {
		subscribe(listener) {
			listeners.add(listener);
			if (listeners.size === 1) start();
			else listener(roster);
			return () => {
				listeners.delete(listener);
				if (listeners.size === 0) stop();
			};
		},
		bubbleFor(sessionId) {
			const found = slots.get(sessionId);
			return found ? composeBubble(found, deps.now()) : null;
		},
		refreshNow,
	};
}
