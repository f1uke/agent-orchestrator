import { applyEvent, emptySlots, type ActivityFrame, type ActivitySlots } from "./activity-decay";
import { composeBubble, type ComposedBubble } from "./bubble-compose";
import type { CompanionActivity, CompanionFeed } from "./feed";

// A hand-driven feed, for the dev playground on `companion.html`.
//
// It is NOT a second implementation of the bubble: frames go through the very same
// `applyEvent`/`composeBubble` the live feed uses, so pressing "say something" in
// the panel exercises the production decay ladder and you can watch a claim fall
// from detail to coarse to silence on the real clock. Only the transport is faked
// — which is the only part a browser cannot have.

export type ManualFeed = CompanionFeed & {
	/** Replace the whole roster. Same snapshot contract as the real feed. */
	setRoster(activities: CompanionActivity[]): void;
	roster(): CompanionActivity[];
	/** Feed one activity frame in, exactly as the SSE would deliver it. */
	push(frame: ActivityFrame): void;
	/** Drop everything this session has said, so it goes quiet immediately. */
	hush(sessionId: string): void;
	bubbleFor(sessionId: string): ComposedBubble | null;
};

export function createManualFeed(initial: CompanionActivity[] = []): ManualFeed {
	const listeners = new Set<(activities: CompanionActivity[]) => void>();
	const slots = new Map<string, ActivitySlots>();
	let roster = initial;

	return {
		subscribe(listener) {
			listeners.add(listener);
			listener(roster);
			return () => listeners.delete(listener);
		},
		setRoster(activities) {
			roster = activities;
			for (const listener of listeners) listener(roster);
		},
		roster: () => roster,
		push(frame) {
			slots.set(frame.sessionId, applyEvent(slots.get(frame.sessionId) ?? emptySlots(), frame));
		},
		hush(sessionId) {
			slots.delete(sessionId);
		},
		bubbleFor(sessionId) {
			const found = slots.get(sessionId);
			return found ? composeBubble(found, Date.now()) : null;
		},
	};
}
