// A stand-in activity source so the overlay and the behaviour engine can be built
// and demonstrated before `GET /api/v1/activity/stream` exists. Five fake sessions
// walk their own status script, chosen so all four behaviour modes appear.
//
// The content is a PURE function of a step index; the only stateful part is the
// interval that advances it. Swapping in the real feed replaces this whole file.

import type { SessionStatus } from "../renderer/types/workspace";
import type { CompanionActivity, CompanionFeed } from "./feed";

/** How long the mock holds each state. Long enough to watch, short enough to demo. */
export const MOCK_FEED_STEP_MS = 20_000;

// One script per fake session, of coprime-ish lengths so the cast does not change
// state in lockstep. Between them they cover anchor (working/idle/todo/ci_failed),
// amble (pr_open/review_pending/…), summon (needs_input) and still (no_signal).
const SCRIPTS: Array<{ sessionId: string; states: SessionStatus[] }> = [
	{ sessionId: "mock-curly", states: ["working", "working", "pr_open", "review_pending", "mergeable"] },
	{ sessionId: "mock-angle", states: ["idle", "needs_input", "needs_input", "working"] },
	{ sessionId: "mock-brack", states: ["todo", "working", "ci_failed", "changes_requested", "approved", "merged"] },
	{ sessionId: "mock-glob", states: ["draft", "pr_open", "no_signal"] },
	{ sessionId: "mock-hash", states: ["working", "idle"] },
];

/** Steps before the whole cast repeats: the lcm of the script lengths. */
export const MOCK_FEED_CYCLE_STEPS = SCRIPTS.reduce((acc, { states }) => lcm(acc, states.length), 1);

function lcm(a: number, b: number): number {
	let x = a;
	let y = b;
	while (y !== 0) [x, y] = [y, x % y];
	return (a / x) * b;
}

/** The roster at a given step. Pure, cyclic, stable session ids. */
export function mockActivitiesAt(step: number): CompanionActivity[] {
	return SCRIPTS.map(({ sessionId, states }) => ({
		sessionId,
		status: states[((step % states.length) + states.length) % states.length],
	}));
}

/** The mock as a {@link CompanionFeed}: an immediate snapshot, then one per step. */
export function createMockFeed(): CompanionFeed {
	return {
		subscribe(listener) {
			let step = 0;
			listener(mockActivitiesAt(step));
			const timer = setInterval(() => {
				step += 1;
				listener(mockActivitiesAt(step));
			}, MOCK_FEED_STEP_MS);
			return () => clearInterval(timer);
		},
	};
}
