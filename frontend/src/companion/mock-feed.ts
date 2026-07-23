// A stand-in activity source so the overlay and the behaviour engine can be built
// and demonstrated before `GET /api/v1/activity/stream` exists. Five fake sessions
// walk their own status script, chosen so all four behaviour modes appear.
//
// The content is a PURE function of a step index; the only stateful part is the
// interval that advances it. Swapping in the real feed replaces this whole file.

import type { SessionStatus } from "../renderer/types/workspace";
import type { CompanionActivity, CompanionFeed } from "./feed";
import type { SessionKind } from "./live-roster";

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
// The NAMES are BOARD NAMES: the short label a person already calls the work by,
// which is what the app's own sidebar shows. They went through two wrong shapes
// first:
// "refactor the parser" read as what the session is DOING, which is the bubble's
// job, and branch names read as plumbing. A board name is what a person already
// calls the work, which is the only thing worth putting under a character.
//
// Everything here is INVENTED. It is demo data that ships in the repo, so it must
// never be lifted from a real board — the shape is what matters, not the content.
//
// Session refs follow the real `<project>-<n>` shape, because the tooltip shows them
// and because the character is a stable hash of the ref. A first set of realistic
// ⚠ Spread across FOUR projects on purpose, with two of them holding more than one
// session. The creature is now chosen by the project, so a mock that put everything on
// one project would draw one animal eight times and demonstrate nothing — and a mock
// with eight projects would draw eight and hide the grouping, which is the actual point.
//
// refs clustered four of eight sessions onto one character — five shared a project
// prefix and differed only in a low number — which is the all-identical complaint
// again, caught by the roster test rather than by eye — so these ids ARE the
// demo's casting decision. They were chosen to spread across the cast, which is not
// cheating: demonstrating the range is the mock's whole job, and mock-feed.test.ts
// holds the roster to it so a later edit cannot quietly put five of eight sessions
// on the same character again.
const SCRIPTS: Array<{
	sessionId: string;
	name: string;
	project: string;
	kind?: SessionKind;
	states: SessionStatus[];
}> = [
	{
		sessionId: "demo-app-59",
		name: "login rate limit",
		project: "demo-app",
		states: ["working", "working", "pr_open", "review_pending", "approved"],
	},
	{
		sessionId: "demo-web-74",
		name: "lint rules",
		project: "demo-web",
		states: ["ci_failed", "working", "ci_failed", "changes_requested"],
	},
	{
		sessionId: "demo-app-48",
		name: "banner cta",
		project: "demo-app",
		states: ["needs_input", "needs_input", "working", "mergeable", "merged"],
	},
	{
		sessionId: "demo-web-35",
		name: "cache warmup",
		project: "demo-web",
		states: ["idle", "idle", "working"],
	},
	{
		sessionId: "demo-infra-70",
		name: "search filters",
		project: "demo-infra",
		states: ["todo", "todo", "working", "draft", "pr_open", "mergeable", "merged"],
	},
	{
		sessionId: "demo-api-86",
		name: "webhook retries",
		project: "demo-api",
		states: ["no_signal", "no_signal", "working", "idle"],
	},
	{
		sessionId: "demo-api-29",
		name: "invoice export",
		project: "demo-api",
		states: ["merged", "terminated", "todo", "working", "approved", "mergeable"],
	},
	{
		// One coordinator, as a real project has: it wears the gold chip and the crown,
		// and there is no seeing either if the demo roster is all workers.
		sessionId: "demo-app-10",
		name: "coordinating demo-app",
		project: "demo-app",
		kind: "orchestrator",
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
	return SCRIPTS.map(({ sessionId, name, project, kind, states }) => ({
		sessionId,
		name,
		project,
		kind: kind ?? "worker",
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
