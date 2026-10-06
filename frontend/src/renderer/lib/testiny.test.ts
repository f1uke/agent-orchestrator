import { describe, expect, it } from "vitest";
import {
	ageLabel,
	evidenceLabel,
	linkedByLabel,
	orderCases,
	runNotice,
	scriptCoverage,
	sharedBlocker,
	summaryCounts,
	type TestinyCase,
	type TestinyRun,
} from "./testiny";

const NOW = Date.parse("2026-10-06T12:00:00Z");
const ago = (minutes: number) => new Date(NOW - minutes * 60_000).toISOString();

const tc = (id: number, status: string, script?: string): TestinyCase => ({
	id,
	title: `case ${id}`,
	status,
	...(script ? { script } : {}),
});

const run = (over: Partial<TestinyRun> = {}): TestinyRun => ({
	link: { sessionId: "task-1", runId: 632, linkedBy: "", createdAt: ago(120) },
	title: "MOBILITY-4839 Chat notice disclaimer - iOS",
	url: "https://app.testiny.io/MOB/testruns/tr/632",
	closed: false,
	counts: null,
	cases: [],
	evidenceDir: "",
	fetchedAt: ago(0),
	...over,
});

describe("orderCases", () => {
	it("lists every case that did not pass first, worst first, and keeps passed cases apart", () => {
		const { open, passed } = orderCases([
			tc(1, "PASSED"),
			tc(2, "NOTRUN"),
			tc(3, "FAILED"),
			tc(4, "PASSED"),
			tc(5, "BLOCKED"),
			tc(6, "NOTRUN"),
		]);
		expect(open.map((c) => c.id)).toEqual([3, 5, 2, 6]);
		expect(passed.map((c) => c.id)).toEqual([1, 4]);
	});

	it("keeps a status Testiny adds later in the open list rather than hiding it as passed", () => {
		const { open, passed } = orderCases([tc(1, "RETEST"), tc(2, "PASSED")]);
		expect(open.map((c) => c.id)).toEqual([1]);
		expect(passed.map((c) => c.id)).toEqual([2]);
	});
});

describe("summaryCounts", () => {
	it("reads the run's counts in a fixed order and hides the zero ones", () => {
		expect(summaryCounts(run({ counts: { NOTRUN: 2, PASSED: 5, FAILED: 1, BLOCKED: 0 } }))).toEqual([
			{ status: "PASSED", count: 5 },
			{ status: "FAILED", count: 1 },
			{ status: "NOTRUN", count: 2 },
		]);
	});

	it("tallies the cases when the run carries no counts", () => {
		expect(summaryCounts(run({ cases: [tc(1, "PASSED"), tc(2, "PASSED"), tc(3, "FAILED")] }))).toEqual([
			{ status: "PASSED", count: 2 },
			{ status: "FAILED", count: 1 },
		]);
	});
});

describe("scriptCoverage", () => {
	it("is absent when no case has a script", () => {
		expect(scriptCoverage([tc(1, "PASSED"), tc(2, "FAILED")])).toBeNull();
	});

	it("counts scripted cases against all cases", () => {
		expect(scriptCoverage([tc(1, "PASSED", "cases/a.yaml"), tc(2, "FAILED"), tc(3, "NOTRUN", "cases/b.yaml")])).toEqual(
			{ scripted: 2, total: 3 },
		);
	});
});

describe("sharedBlocker", () => {
	const failing = (kind: "auth" | "binary_missing" | "unavailable") => run({ fetchError: { kind, message: "x" } });

	it("names the problem when every run failed for the same machine-wide reason", () => {
		expect(sharedBlocker([failing("auth"), failing("auth")])).toBe("auth");
		expect(sharedBlocker([failing("binary_missing")])).toBe("binary_missing");
	});

	it("is null when a run read fine, when the reasons differ, or for a per-run reason", () => {
		expect(sharedBlocker([failing("auth"), run()])).toBeNull();
		expect(sharedBlocker([failing("auth"), failing("binary_missing")])).toBeNull();
		expect(sharedBlocker([failing("unavailable"), failing("unavailable")])).toBeNull();
		expect(sharedBlocker([])).toBeNull();
	});
});

describe("runNotice", () => {
	it("is null for a run read fine", () => {
		expect(runNotice(run(), NOW, null)).toBeNull();
	});

	it("says a deleted run is gone", () => {
		const r = run({ fetchError: { kind: "not_found", message: "404" } });
		expect(runNotice(r, NOW, null)).toBe("Run not found in Testiny (deleted?)");
	});

	it("says how old the data on screen is, and why it is not newer", () => {
		const r = run({ fetchedAt: ago(4), fetchError: { kind: "unavailable", message: "timeout" } });
		expect(runNotice(r, NOW, null)).toBe("Testiny did not answer · showing data from 4 min ago");
	});

	it("leaves the reason to the banner when the banner already gives it", () => {
		const r = run({ fetchedAt: ago(4), fetchError: { kind: "auth", message: "403" } });
		expect(runNotice(r, NOW, "auth")).toBe("showing data from 4 min ago");
	});

	it("says the run was never read when there is no earlier data", () => {
		const r = run({ fetchedAt: undefined, fetchError: { kind: "rejected", message: "400" } });
		expect(runNotice(r, NOW, null)).toBe("Testiny refused the read · couldn't read this run yet");
	});
});

describe("ageLabel", () => {
	it.each([
		[0, "just now"],
		[1, "1 min ago"],
		[59, "59 min ago"],
		[120, "2 h ago"],
		[60 * 50, "2 d ago"],
	])("%i minutes reads as %s", (minutes, label) => {
		expect(ageLabel(ago(minutes), NOW)).toBe(label);
	});
});

describe("linkedByLabel", () => {
	const sessions = [
		{ id: "task-1", crew: { id: "task-1", role: "dev" as const, hasRun: true } },
		{ id: "task-1-qa", crew: { id: "task-1", role: "qa" as const, hasRun: true } },
		{ id: "solo-9" },
	];

	it("names the person when nobody's session linked it", () => {
		expect(linkedByLabel("", sessions)).toBe("you");
	});

	it("names a crew member by its role", () => {
		expect(linkedByLabel("task-1-qa", sessions)).toBe("qa");
		expect(linkedByLabel("task-1", sessions)).toBe("dev");
	});

	it("falls back to the session id for a session that is not on the board", () => {
		expect(linkedByLabel("solo-9", sessions)).toBe("solo-9");
		expect(linkedByLabel("gone-3", sessions)).toBe("gone-3");
	});
});

describe("evidenceLabel", () => {
	it("shortens a QA Evidence path to the tree root and keeps the run's folder apart", () => {
		expect(
			evidenceLabel("/Users/fluke/Desktop/QA Evidence/MOBILITY/2026/MOBILITY 2026-19/TP-193 Chat/TR-632 - iOS"),
		).toEqual({ location: "~/Desktop/QA Evidence/…/", folder: "TR-632 - iOS" });
	});

	it("only abbreviates the home folder of any other path", () => {
		expect(evidenceLabel("/Users/fluke/elsewhere/TR-1 - x")).toEqual({ location: "~/elsewhere/", folder: "TR-1 - x" });
	});
});
