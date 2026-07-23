import { describe, expect, it } from "vitest";
import { isGenuinelyWaiting, sessionsToActivities, type LiveSession } from "./live-roster";

function session(over: Partial<LiveSession> = {}): LiveSession {
	return {
		id: "demo-app-1",
		name: "add login",
		projectName: "demo-app",
		kind: "worker",
		status: "working",
		isTerminated: false,
		...over,
	};
}

describe("isGenuinelyWaiting", () => {
	// The single most important distinction the overlay makes. `waiting_input` is a
	// REAL permission prompt the agent raised. `idle_aged` / `active_stale` are AO's
	// timeout GUESSES — it has been quiet a while, so we suspect. Both land on the
	// same `needs_input` status, and only the first has earned the right to walk to
	// the front and ask for attention.

	it("is true for a real prompt the agent raised", () => {
		expect(isGenuinelyWaiting(session({ status: "needs_input", statusReason: "waiting_input" }))).toBe(true);
	});

	it("is FALSE when we only inferred it from silence", () => {
		expect(isGenuinelyWaiting(session({ status: "needs_input", statusReason: "idle_aged" }))).toBe(false);
		expect(isGenuinelyWaiting(session({ status: "needs_input", statusReason: "active_stale" }))).toBe(false);
	});

	it("is false when we have no reason at all — absence is not evidence", () => {
		expect(isGenuinelyWaiting(session({ status: "needs_input" }))).toBe(false);
	});

	it("is false for any status that is not asking for you", () => {
		expect(isGenuinelyWaiting(session({ status: "working", statusReason: "waiting_input" }))).toBe(false);
	});
});

describe("sessionsToActivities", () => {
	it("turns live sessions into the roster the engine already understands", () => {
		const roster = sessionsToActivities([
			session({ id: "demo-app-1", name: "add login", projectName: "demo-app", status: "working" }),
			session({ id: "demo-api-2", name: "fix retries", projectName: "demo-api", status: "idle" }),
		]);

		expect(roster).toEqual([
			{ sessionId: "demo-app-1", status: "working", name: "add login", project: "demo-app", kind: "worker" },
			{ sessionId: "demo-api-2", status: "idle", name: "fix retries", project: "demo-api", kind: "worker" },
		]);
	});

	it("carries the coordinator through, so its Proc can be marked", () => {
		const roster = sessionsToActivities([session({ id: "demo-app-9", kind: "orchestrator" })]);

		expect(roster[0].kind).toBe("orchestrator");
	});

	it("drops terminated sessions, because a finished session is not on your desktop", () => {
		const roster = sessionsToActivities([session({ id: "gone", isTerminated: true }), session({ id: "here" })]);

		expect(roster.map((r) => r.sessionId)).toEqual(["here"]);
	});

	it("keeps a genuinely-waiting session at needs_input so it comes to the front", () => {
		const roster = sessionsToActivities([session({ status: "needs_input", statusReason: "waiting_input" })]);

		expect(roster[0].status).toBe("needs_input");
	});

	it("calms a session we only GUESSED was waiting, so it does not cry wolf", () => {
		// It still shows as quiet — no_signal is the honest scene for "we have not
		// heard from it" — but it does not walk to the front demanding attention.
		const roster = sessionsToActivities([session({ status: "needs_input", statusReason: "idle_aged" })]);

		expect(roster[0].status).toBe("no_signal");
	});

	// The roster reports every session that is ALIVE. It deliberately does not trim
	// itself to what the desktop has room to draw: further down, a session missing from
	// this list is how the END of a session is recognised, so a session left out because
	// the band was full was seen out through a portal — a ring closing over a session
	// that was still working. How many Procs fit is answered against the band itself, by
	// `MAX_PETS`/`bandMembers` in `behaviour.ts`, which is the only place that knows
	// which sessions already have a Proc.
	it("reports every live session, however many there are", () => {
		const many = Array.from({ length: 40 }, (_, i) => session({ id: `s${i}` }));

		expect(sessionsToActivities(many)).toHaveLength(40);
	});
});
