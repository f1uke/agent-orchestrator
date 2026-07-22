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

// Eight fake sessions, each on its own script, with coprime-ish lengths so the
// cast never changes state in lockstep.
//
// The scripts are STAGGERED rather than parallel: each one starts at a different
// point in the lifecycle, so any single frame shows five or more different states
// side by side. That matters because this mock is how the overlay gets looked at —
// the first build read as "the pets don't show anything" partly because the roster
// it was driven by was mostly one state, and correct art looks broken when every
// figure on screen is doing the same thing.
//
// The NAMES are branch-style on purpose. The first set read as "refactor the
// parser", "bump the deps" — which is what a session is DOING, and that is the
// bubble's job. A name has to be an identity you can learn and point at, or the
// chip and the bubble end up saying the same kind of thing twice.
//
// The ids are deliberately varied strings, not `mock-1..8`: the character comes
// from a stable hash of the ref, so the ids ARE the demo's casting decision. They
// were chosen to spread across the cast, which is not cheating — demonstrating the
// range is the mock's whole job, and mock-feed.test.ts holds the roster to it so a
// later edit cannot quietly put five of eight sessions on the same character again.
const SCRIPTS: Array<{ sessionId: string; name: string; project: string; states: SessionStatus[] }> = [
	{
		sessionId: "worker-refactor-parser",
		name: "feature/parser-rewrite",
		project: "agent-orchestrator",
		states: ["working", "working", "pr_open", "review_pending", "approved"],
	},
	{
		sessionId: "worker-flaky-test",
		name: "fix/flaky-spawn-test",
		project: "agent-orchestrator",
		states: ["ci_failed", "working", "ci_failed", "changes_requested"],
	},
	{
		sessionId: "worker-ask-the-human",
		name: "feature/board-columns",
		project: "design-system",
		states: ["needs_input", "needs_input", "working", "mergeable", "merged"],
	},
	{
		sessionId: "worker-nap-time",
		name: "chore/dep-bump",
		project: "agent-orchestrator",
		states: ["idle", "idle", "working"],
	},
	{
		sessionId: "worker-backlog-item",
		name: "feature/export-button",
		project: "design-system",
		states: ["todo", "todo", "working", "draft", "pr_open", "mergeable", "merged"],
	},
	{
		sessionId: "worker-lost-contact",
		name: "chore/config-migration",
		project: "infra-tools",
		states: ["no_signal", "no_signal", "working", "idle"],
	},
	{
		sessionId: "worker-shipped-it",
		name: "docs/changelog",
		project: "infra-tools",
		states: ["merged", "terminated", "todo", "working", "approved", "mergeable"],
	},
	{
		sessionId: "worker-odd-one-out",
		name: "investigate/slow-query",
		project: "agent-orchestrator",
		states: ["unknown", "draft", "review_pending", "changes_requested", "no_signal"],
	},
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
	return SCRIPTS.map(({ sessionId, name, project, states }) => ({
		sessionId,
		name,
		project,
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
