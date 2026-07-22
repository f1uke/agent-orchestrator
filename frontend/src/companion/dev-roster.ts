import type { SessionStatus } from "../renderer/types/workspace";
import { castForSession, HATS, PALETTES } from "./cast";
import type { CompanionActivity } from "./feed";
import { ALL_COMPANION_STATUSES } from "./scene";

// The playground's invented roster. Never lifted from a real board — the SHAPE is
// what matters here, not the content.
//
// Its one job beyond being fake is to be VARIED: a demo whose sessions all land
// on the same character is how "why do they all look the same" got reported in
// the first place, and it was the demo data at fault rather than the art.

const NAMES = [
	"login rate limit",
	"banner cta",
	"search filters",
	"cache warmup",
	"invoice export",
	"webhook retries",
	"onboarding tour",
	"lint rules",
	"coupon search ui",
	"smoke to testiny",
	"dark mode audit",
	"session replay",
	"csv import limits",
	"password reset flow",
	"stale branch sweep",
	"receipt pdf layout",
];

// Several, because a single project makes the per-project marker untestable by eye
// — the whole point of it is telling two apart.
const PROJECTS = ["demo-app", "demo-api", "demo-web", "demo-infra", "demo-tools"];

/**
 * A session ref that lands on a CHOSEN character and has not been used yet.
 *
 * The character is a hash of the ref, so a run of consecutive refs puts half the
 * cast on one face. Searching for a ref that hashes where we want it fixes that —
 * but the search must also skip refs already handed out, because two indices
 * wanting the same character scan overlapping ranges and can land on the SAME
 * ref. That produced a roster with 27 entries and 11 distinct sessions: duplicate
 * Procs stacked on one spot, duplicate name chips, and a screen full of collided
 * bubbles.
 */
function refForCharacter(index: number, project: string, taken: Set<string>): string {
	// Both axes wanted, stepped at different rates, so a demo roster covers all the
	// colours AND all the hats rather than six pairs of them.
	const wantPalette = PALETTES[index % PALETTES.length].id;
	// Advanced by the ROW as well as the column, so the pairing shifts each time the
	// colours come round. Stepping both off `index` alone repeats the same six pairs
	// for ever, which is the bundled cast again wearing a different hat.
	const wantHat = HATS[(index + Math.floor(index / PALETTES.length)) % HATS.length].id;
	for (let n = 10 + index; n < 4000; n += 1) {
		const ref = `${project}-${n}`;
		if (taken.has(ref)) continue;
		const look = castForSession(ref);
		if (look.palette === wantPalette && look.hatId === wantHat) return ref;
	}
	// No ref left that hashes where we wanted. A duplicate would be far worse than
	// a repeated look, so take the first free ref of any look at all.
	for (let n = 10 + index; n < 4000; n += 1) {
		const ref = `${project}-${n}`;
		if (!taken.has(ref)) return ref;
	}
	return `${project}-${index}`;
}

/** One demo session. `taken` accumulates across a roster so no ref is used twice. */
export function makeActivity(index: number, status: SessionStatus, taken: Set<string>): CompanionActivity {
	const project = PROJECTS[index % PROJECTS.length];
	const sessionId = refForCharacter(index, project, taken);
	taken.add(sessionId);
	return {
		sessionId,
		name: NAMES[index % NAMES.length],
		project,
		// One coordinator, like a real project, so the mark on its label is visible.
		kind: index === 0 ? "orchestrator" : "worker",
		status,
	};
}

/** A roster of `count` sessions, all working. */
export function demoRoster(count: number): CompanionActivity[] {
	const taken = new Set<string>();
	return Array.from({ length: count }, (_, index) => makeActivity(index, "working", taken));
}

/** One Proc per status: the whole vocabulary on screen at once. */
export function everyStatus(): CompanionActivity[] {
	const taken = new Set<string>();
	return ALL_COMPANION_STATUSES.map((status, index) => makeActivity(index, status, taken));
}

/** One more session on the end of an existing roster, clear of every ref in it. */
export function appendActivity(roster: CompanionActivity[]): CompanionActivity[] {
	const taken = new Set(roster.map((entry) => entry.sessionId));
	return [...roster, makeActivity(roster.length, "working", taken)];
}
