import { describe, expect, it } from "vitest";
import {
	CREW_RUN_MAX_ATTEMPTS,
	type CrewRun,
	crewRunDuration,
	crewRunEscalated,
	crewRunMeta,
	crewRunState,
	crewRunTitle,
	discardStreak,
} from "./crew-run";

const base = {
	sessionId: "w1",
	projectId: "p",
	attempt: 1,
	detector: "live",
	genAtStart: 0,
	genAtEnd: 0,
	startedAt: "2026-08-21T10:00:00Z",
	createdAt: "2026-08-21T10:00:00Z",
	updatedAt: "2026-08-21T10:00:00Z",
	kind: "test",
};

const run = (over: Partial<CrewRun> & { id: string }): CrewRun => ({ ...base, ...over }) as CrewRun;

describe("crewRunState", () => {
	it("reads a run whose tree moved as DISCARDED, never as the result it reported", () => {
		// This is the whole point of the third state. A run that read a mixture of
		// two tree states must not borrow the pass it happened to print: failing it
		// would blame the code, and passing it is the laundering the detector
		// exists to stop.
		const discarded = run({ id: "a", endedAt: "2026-08-21T10:01:00Z", outcome: "discarded", result: "pass" });
		expect(crewRunState(discarded)).toBe("discarded");
		expect(crewRunMeta(discarded).label).toBe("Discarded");
	});

	it("reads a run nothing watched as UNCERTIFIED, never as passed", () => {
		const unwatched = run({ id: "a", endedAt: "2026-08-21T10:01:00Z", outcome: "uncertified", result: "pass" });
		expect(crewRunState(unwatched)).toBe("uncertified");
	});

	it("keeps the reported verdict when the tree held still", () => {
		expect(crewRunState(run({ id: "a", endedAt: "x", outcome: "trusted", result: "pass" }))).toBe("passed");
		expect(crewRunState(run({ id: "b", endedAt: "x", outcome: "trusted", result: "fail" }))).toBe("failed");
		expect(crewRunState(run({ id: "c", endedAt: "x", outcome: "trusted" }))).toBe("finished");
	});

	it("reads an unended run as running", () => {
		expect(crewRunState(run({ id: "a" }))).toBe("running");
	});
});

describe("discardStreak", () => {
	const discarded = (id: string) => run({ id, endedAt: "2026-08-21T10:01:00Z", outcome: "discarded" });
	const trusted = (id: string) => run({ id, endedAt: "2026-08-21T10:01:00Z", outcome: "trusted", result: "pass" });

	it("counts only the CURRENT streak, newest first", () => {
		expect(discardStreak([discarded("c"), discarded("b"), trusted("a"), discarded("z")])).toBe(2);
	});

	it("is cleared by one run that ends any other way", () => {
		expect(discardStreak([trusted("c"), discarded("b"), discarded("a")])).toBe(0);
	});

	it("skips an uncertified run rather than letting it clear the streak", () => {
		// Nothing watched the tree during that run, so it is no evidence the member
		// got a quiet window. If it cleared the streak, a daemon restart would
		// silently cancel the escalation.
		const unwatched = run({ id: "u", endedAt: "2026-08-21T10:01:00Z", outcome: "uncertified" });
		expect(discardStreak([unwatched, discarded("c"), discarded("b"), discarded("a")])).toBe(3);
	});

	it("skips an open run rather than letting it hide the streak", () => {
		// The member's next attempt is already in flight. Treating "not yet
		// decided" as "fine" would make the escalation flicker away the moment
		// the retry started, which is exactly when a person needs to see it.
		const streak = discardStreak([run({ id: "live" }), discarded("c"), discarded("b"), discarded("a")]);
		expect(streak).toBe(3);
	});

	it("escalates at the cap and not before", () => {
		const runs = Array.from({ length: CREW_RUN_MAX_ATTEMPTS }, (_, i) => discarded(`d${i}`));
		expect(crewRunEscalated(runs.slice(0, CREW_RUN_MAX_ATTEMPTS - 1))).toBe(false);
		expect(crewRunEscalated(runs)).toBe(true);
	});

	it("reports nothing for a session that never bracketed a run", () => {
		expect(discardStreak([])).toBe(0);
		expect(crewRunEscalated([])).toBe(false);
	});
});

describe("crewRunDuration", () => {
	it("measures a finished run end to end", () => {
		const r = run({ id: "a", startedAt: "2026-08-21T10:00:00Z", endedAt: "2026-08-21T10:01:30Z" });
		expect(crewRunDuration(r, Date.parse("2026-08-21T11:00:00Z"))).toBe("1m 30s");
	});

	it("measures an open run against now", () => {
		const r = run({ id: "a", startedAt: "2026-08-21T10:00:00Z" });
		expect(crewRunDuration(r, Date.parse("2026-08-21T10:00:20Z"))).toBe("20s");
	});
});

describe("crewRunTitle", () => {
	it("names the command when the member gave one", () => {
		expect(crewRunTitle(run({ id: "a", kind: "test", label: "go test ./..." }))).toBe("Test · go test ./...");
	});

	it("falls back to the kind alone", () => {
		expect(crewRunTitle(run({ id: "a", kind: "build" }))).toBe("Build");
	});
});
