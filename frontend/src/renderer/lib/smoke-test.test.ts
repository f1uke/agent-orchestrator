import { describe, expect, it } from "vitest";
import {
	activeChecks,
	agentState,
	authorLabel,
	checklistState,
	isAgentStale,
	progressFor,
	retiredChecks,
	standDownActor,
	type SmokeCheck,
	type SmokeStandDown,
} from "./smoke-test";

const base = {
	sessionId: "w1",
	projectId: "p",
	why: "",
	steps: [],
	expected: "",
	prNum: 0,
	fileRef: "",
	note: "",
	evidence: [],
	agentEvidence: [],
	createdAt: "2026-08-20T00:00:00Z",
	updatedAt: "2026-08-20T00:00:00Z",
};

const check = (over: Partial<SmokeCheck> & { id: string }): SmokeCheck =>
	({ seq: 1, name: over.id, verdict: "pending", ...base, ...over }) as SmokeCheck;

describe("progressFor", () => {
	it("counts the user's verdicts, and only the user's", () => {
		// A machine pass is not a pass. If it were counted as one the checklist
		// would read complete with nobody having opened the app.
		const p = progressFor([
			check({ id: "a", verdict: "pass" }),
			check({ id: "b", verdict: "pending", agentVerdict: "pass" }),
		]);
		expect(p.total).toBe(2);
		expect(p.pass).toBe(1);
		expect(p.pending).toBe(1);
		expect(p.checked).toBe(1);
		expect(p.agentPass).toBe(1);
	});

	it("counts a machine's verdict only while the person has not decided", () => {
		// The person judged the case themselves; a machine's older answer must
		// stop being counted rather than going on shouting.
		const p = progressFor([check({ id: "a", verdict: "pass", agentVerdict: "fail" })]);
		expect(p.agentFail).toBe(0);
		expect(p.pass).toBe(1);
	});

	it("leaves retired cases out of the checklist it counts", () => {
		const p = progressFor([
			check({ id: "a", verdict: "pass" }),
			check({ id: "b", verdict: "fail", retiredAt: "2026-08-20T00:00:00Z", retiredReason: "covered by tests" }),
		]);
		expect(p.total).toBe(1);
		expect(p.retired).toBe(1);
		// The retired case's old failure is not the checklist's failure any more.
		expect(p.fail).toBe(0);
		expect(p.pending).toBe(0);
		expect(p.checked).toBe(1);
	});
});

describe("agentState", () => {
	it("reads a run with no verdict as captured, not as never-run", () => {
		// The state this vocabulary exists for: qa walked to the screen and
		// photographed it, then declined to judge because paint / focus / timing /
		// feel are not machine-judgeable. Collapsing it into "not run" would send
		// the person looking for a machine verdict that is never coming.
		expect(agentState(check({ id: "a", agentRanAt: "2026-08-20T09:00:00Z" }))).toBe("captured");
		expect(agentState(check({ id: "b", agentRanAt: "2026-08-20T09:00:00Z", agentVerdict: "" }))).toBe("captured");
		expect(agentState(check({ id: "c" }))).toBe("none");
	});

	it("reads the three machine verdicts", () => {
		const at = "2026-08-20T09:00:00Z";
		expect(agentState(check({ id: "a", agentRanAt: at, agentVerdict: "pass" }))).toBe("pass");
		expect(agentState(check({ id: "b", agentRanAt: at, agentVerdict: "fail" }))).toBe("fail");
		expect(agentState(check({ id: "c", agentRanAt: at, agentVerdict: "skip" }))).toBe("skip");
	});
});

describe("progressFor, machine lane", () => {
	it("counts a captured case apart from a machine verdict", () => {
		const p = progressFor([
			check({ id: "a", agentRanAt: "2026-08-20T09:00:00Z" }),
			check({ id: "b", agentRanAt: "2026-08-20T09:00:00Z", agentVerdict: "pass" }),
		]);
		expect(p.agentCaptured).toBe(1);
		expect(p.agentPass).toBe(1);
		// Neither is progress: both cases are still the person's to play.
		expect(p.checked).toBe(0);
		expect(p.pending).toBe(2);
	});
});

describe("isAgentStale", () => {
	const HEAD = "4b21e07c9a5d1f6083e2b7c4419af6d2e0d5c118";
	const OLD = "9f0c2ad41b77e3b5c8d6a0f21e4c7b9038a1d6e5";
	const at = "2026-08-20T09:00:00Z";

	it("fires when the machine ran against a commit that is no longer head", () => {
		const c = check({ id: "a", prNum: 322, agentRanAt: at, agentVerdict: "pass", agentSha: OLD });
		expect(isAgentStale(c, [{ number: 322, headSha: HEAD }])).toBe(true);
	});

	it("does not fire when the run is at head", () => {
		const c = check({ id: "a", prNum: 322, agentRanAt: at, agentVerdict: "pass", agentSha: HEAD });
		expect(isAgentStale(c, [{ number: 322, headSha: HEAD }])).toBe(false);
	});

	it("treats an abbreviated sha as the same commit rather than crying stale", () => {
		const c = check({ id: "a", prNum: 322, agentRanAt: at, agentVerdict: "pass", agentSha: HEAD.slice(0, 8) });
		expect(isAgentStale(c, [{ number: 322, headSha: HEAD }])).toBe(false);
	});

	it("says nothing when there is nothing to compare against", () => {
		// No head, no recorded sha, or no machine run at all: "cannot tell" must
		// render as a normal case, never as a warning.
		const ran = check({ id: "a", agentRanAt: at, agentVerdict: "pass", agentSha: OLD });
		expect(isAgentStale(ran, [])).toBe(false);
		expect(isAgentStale(check({ id: "b", agentRanAt: at, agentVerdict: "pass" }), [{ number: 1, headSha: HEAD }])).toBe(
			false,
		);
		expect(isAgentStale(check({ id: "c", agentSha: OLD }), [{ number: 1, headSha: HEAD }])).toBe(false);
	});

	it("compares against the PR the case names, not just the first one", () => {
		const c = check({ id: "a", prNum: 322, agentRanAt: at, agentVerdict: "pass", agentSha: OLD });
		const heads = [
			{ number: 319, headSha: HEAD },
			{ number: 322, headSha: OLD },
		];
		expect(isAgentStale(c, heads)).toBe(false);
	});
});

describe("activeChecks / retiredChecks", () => {
	it("splits the play list from the record", () => {
		const live = check({ id: "live" });
		const gone = check({ id: "gone", retiredAt: "2026-08-19T00:00:00Z", retiredReason: "covered by a Go test" });
		expect(activeChecks([live, gone]).map((c) => c.id)).toEqual(["live"]);
		expect(retiredChecks([live, gone]).map((c) => c.id)).toEqual(["gone"]);
	});

	it("orders the retired pile newest first", () => {
		const a = check({ id: "a", retiredAt: "2026-08-10T00:00:00Z", retiredReason: "r" });
		const b = check({ id: "b", retiredAt: "2026-08-19T00:00:00Z", retiredReason: "r" });
		expect(retiredChecks([a, b]).map((c) => c.id)).toEqual(["b", "a"]);
	});
});

const stood = (over: Partial<SmokeStandDown> = {}): SmokeStandDown =>
	({
		sessionId: "w1",
		at: "2026-08-20T00:00:00Z",
		reason: "pure refactor",
		createdAt: "2026-08-20T00:00:00Z",
		updatedAt: "2026-08-20T00:00:00Z",
		...over,
	}) as SmokeStandDown;

describe("authorLabel", () => {
	it("prefers the crew role, which is what a reader is actually asking", () => {
		expect(authorLabel(check({ id: "a", authoredBy: "mer-1", authoredByRole: "dev" }))).toBe("written by dev");
	});

	it("falls back to the session id for an author with no role", () => {
		expect(authorLabel(check({ id: "a", authoredBy: "solo-7" }))).toBe("written by @solo-7");
	});

	// A guessed author on a list whose whole point is telling two authors apart
	// would be worse than none.
	it("names nobody when AO could not identify the author", () => {
		expect(authorLabel(check({ id: "a" }))).toBe("");
	});
});

describe("checklistState", () => {
	// The distinction the tab could not previously draw.
	it("tells an undecided checklist from a stood-down one", () => {
		expect(checklistState([], null)).toBe("undecided");
		expect(checklistState([], stood())).toBe("stood-down");
	});

	it("shows the cases whenever there are any to play", () => {
		expect(checklistState([check({ id: "a" })], null)).toBe("cases");
		// Even alongside a stand-down: a case on the list disproves the claim, so
		// the list wins for the one poll a stale cache could carry both.
		expect(checklistState([check({ id: "a" })], stood())).toBe("cases");
	});

	// An all-retired checklist has nothing to play but is not an absence: the
	// retired cases and their reasons are still listed below it.
	it("keeps all-retired distinct from both", () => {
		expect(checklistState([check({ id: "a", retiredAt: "2026-08-21T00:00:00Z" })], null)).toBe("all-retired");
	});
});

describe("standDownActor", () => {
	it("names the role, then the session, then falls back to the worker", () => {
		expect(standDownActor(stood({ byRole: "qa", by: "mer-2" }))).toBe("qa");
		expect(standDownActor(stood({ by: "solo-7" }))).toBe("@solo-7");
		expect(standDownActor(stood())).toBe("the worker");
	});
});
