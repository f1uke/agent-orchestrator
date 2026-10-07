import { describe, expect, it } from "vitest";
import {
	ageLabel,
	caseChips,
	caseFacts,
	evidenceLabel,
	linkedByLabel,
	orderCases,
	provenanceLabel,
	resultInput,
	runNotice,
	scriptCoverage,
	sharedBlocker,
	stepStatus,
	summaryCounts,
	textBlocks,
	unlinkedTaskIssue,
	withCase,
	withResult,
	type TestinyCase,
	type TestinyCaseDetail,
	type TestinyRun,
} from "./testiny";

const NOW = Date.parse("2026-10-06T12:00:00Z");
const ago = (minutes: number) => new Date(NOW - minutes * 60_000).toISOString();

const tc = (id: number, status: string, script?: string): TestinyCase => ({
	id,
	title: `case ${id}`,
	status,
	steps: [],
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

describe("orderCases keeping a held order", () => {
	it("keeps every case where it was, whatever its status is now", () => {
		const before = orderCases([tc(1, "PASSED"), tc(2, "NOTRUN"), tc(3, "FAILED")]);
		const now = orderCases([tc(1, "FAILED"), tc(2, "PASSED"), tc(3, "FAILED")], {
			open: before.open.map((c) => c.id),
			passed: before.passed.map((c) => c.id),
		});
		expect(now.open.map((c) => [c.id, c.status])).toEqual([
			[3, "FAILED"],
			[2, "PASSED"],
		]);
		expect(now.passed.map((c) => [c.id, c.status])).toEqual([[1, "FAILED"]]);
	});

	it("places a case the held order does not know by the usual rule", () => {
		const { open, passed } = orderCases([tc(1, "NOTRUN"), tc(2, "FAILED"), tc(3, "PASSED")], {
			open: [1],
			passed: [],
		});
		expect(open.map((c) => c.id)).toEqual([1, 2]);
		expect(passed.map((c) => c.id)).toEqual([3]);
	});
});

describe("withResult", () => {
	const at = ago(0);

	it("sets the case's status, moves one count across, and records it as the person's", () => {
		const next = withResult(
			run({ counts: { PASSED: 1, NOTRUN: 2 }, cases: [tc(1, "PASSED"), tc(2, "NOTRUN"), tc(3, "NOTRUN")] }),
			{ caseId: 2, status: "FAILED", comment: "ปุ่มแชร์ไม่ขึ้น" },
			at,
		);
		expect(next.cases[1]).toEqual({
			...tc(2, "FAILED"),
			recorded: { status: "FAILED", comment: "ปุ่มแชร์ไม่ขึ้น", by: "", sha: "", at },
		});
		expect(next.counts).toEqual({ PASSED: 1, NOTRUN: 1, FAILED: 1 });
	});

	it("leaves counts Testiny did not give to be counted from the cases", () => {
		const next = withResult(run({ cases: [tc(1, "NOTRUN")] }), { caseId: 1, status: "PASSED" }, at);
		expect(next.counts).toBeNull();
		expect(summaryCounts(next)).toEqual([{ status: "PASSED", count: 1 }]);
	});

	it("does not touch the run when the case is not in it", () => {
		const before = run({ counts: { NOTRUN: 1 }, cases: [tc(1, "NOTRUN")] });
		expect(withResult(before, { caseId: 9, status: "PASSED" }, at)).toBe(before);
	});
});

describe("a step's result", () => {
	const step = (n: number, rid: string) => ({ n, rid, action: `step ${n}`, expected: "" });
	const held = {
		...tc(1, "FAILED"),
		steps: [
			{ n: 1, rid: "b", status: "PASSED" },
			{ n: 3, rid: "", status: "BLOCKED" },
		],
	};

	it("is the one Testiny holds for the step's row id, wherever the step has moved", () => {
		expect(stepStatus(held, step(2, "b"))).toBe("PASSED");
		expect(stepStatus(held, step(1, "a"))).toBe("NOTRUN");
	});

	it("goes by number when either side has no row id, and is Not run when Testiny holds none", () => {
		expect(stepStatus(held, step(3, "c"))).toBe("BLOCKED");
		expect(stepStatus(tc(2, "NOTRUN"), step(1, "a"))).toBe("NOTRUN");
	});

	it("is set on screen without touching the case's own status, counts or record", () => {
		const recorded = { status: "FAILED", comment: "x", by: "qa-1", sha: "", at: ago(5) };
		const before = run({ counts: { FAILED: 1 }, cases: [{ ...held, recorded }] });
		const next = withResult(before, { caseId: 1, step: { n: 2, rid: "b" }, status: "FAILED" }, ago(0));
		expect(next.cases[0]).toEqual({
			...held,
			recorded,
			steps: [
				{ n: 2, rid: "b", status: "FAILED" },
				{ n: 3, rid: "", status: "BLOCKED" },
			],
		});
		expect(next.counts).toEqual({ FAILED: 1 });
	});

	it("is sent to the daemon as the case's steps with no case status", () => {
		expect(resultInput({ caseId: 7, step: { n: 2, rid: "b" }, status: "FAILED" })).toEqual({
			caseId: 7,
			steps: [{ n: 2, status: "FAILED" }],
		});
		expect(resultInput({ caseId: 7, status: "BLOCKED", comment: "no device" })).toEqual({
			caseId: 7,
			status: "BLOCKED",
			comment: "no device",
		});
	});
});

describe("withCase", () => {
	it("puts a case back as it was, counts included, so a failed write rolls back", () => {
		const before = run({ counts: { NOTRUN: 1, PASSED: 1 }, cases: [tc(1, "NOTRUN"), tc(2, "PASSED")] });
		const written = withResult(before, { caseId: 1, status: "PASSED" }, ago(0));
		const back = withCase(written, before.cases[0]);
		expect(back.cases).toEqual(before.cases);
		expect(summaryCounts(back)).toEqual(summaryCounts(before));
	});
});

describe("provenanceLabel", () => {
	const sessions = [{ id: "task-1-qa", crew: { id: "task-1", role: "qa" as const, hasRun: true } }];
	const recorded = (over: Partial<NonNullable<TestinyCase["recorded"]>> = {}) => ({
		status: "FAILED",
		comment: "x",
		by: "task-1-qa",
		byRole: "qa" as const,
		sha: "4f2c9e1d0b7a",
		at: ago(5),
		...over,
	});

	it("names the agent's role, when, and the commit it tested", () => {
		expect(provenanceLabel({ ...tc(1, "FAILED"), recorded: recorded() }, NOW, sessions)).toBe(
			"set by qa · 5 min ago · on 4f2c9e1",
		);
	});

	it("says you for a result set in the app, with no commit", () => {
		expect(
			provenanceLabel(
				{
					...tc(1, "PASSED"),
					recorded: recorded({ status: "PASSED", by: "", byRole: undefined, sha: "", at: ago(2) }),
				},
				NOW,
				sessions,
			),
		).toBe("set by you · 2 min ago");
	});

	it("falls back to the session for a solo worker", () => {
		expect(
			provenanceLabel({ ...tc(1, "FAILED"), recorded: recorded({ by: "solo-7", byRole: undefined }) }, NOW, sessions),
		).toBe("set by solo-7 · 5 min ago · on 4f2c9e1");
	});

	it("says nothing when AO wrote nothing, or Testiny changed the case since", () => {
		expect(provenanceLabel(tc(1, "FAILED"), NOW, sessions)).toBeNull();
		expect(provenanceLabel({ ...tc(1, "PASSED"), recorded: recorded() }, NOW, sessions)).toBeNull();
	});
});

const detail = (over: Partial<TestinyCaseDetail> = {}): TestinyCaseDetail => ({
	id: 7102,
	title: "Empty state",
	template: "STEPS",
	type: "",
	platforms: [],
	jira: "",
	features: "",
	subFeatures: "",
	section: "",
	automation: [],
	testData: "",
	precondition: "",
	description: "",
	remark: "",
	steps: [],
	stepsText: "",
	expectedText: "",
	bdd: "",
	requirements: [],
	...over,
});

describe("caseChips", () => {
	it("lists what the case is about in reading order, each with the field it came from", () => {
		expect(
			caseChips(
				detail({
					priority: { level: 2, label: "High" },
					type: "FUNCTIONAL",
					platforms: ["iOS", "Android"],
					jira: "MOBILITY-4839",
					features: "Share",
					subFeatures: "Empty state",
					section: "Fund page",
					automation: ["Manual"],
				}),
			),
		).toEqual([
			{ field: "Priority", value: "High" },
			{ field: "Type", value: "Functional" },
			{ field: "Platform", value: "iOS" },
			{ field: "Platform", value: "Android" },
			{ field: "Jira", value: "MOBILITY-4839" },
			{ field: "Automation", value: "Manual" },
		]);
	});

	it("leaves out what the case does not set", () => {
		expect(caseChips(detail())).toEqual([]);
		expect(caseChips(detail({ subFeatures: "Empty state", type: "NON_FUNCTIONAL" }))).toEqual([
			{ field: "Type", value: "Non functional" },
		]);
	});
});

describe("caseFacts", () => {
	it("puts the long fields on their own rows, feature then section", () => {
		expect(caseFacts(detail({ features: "Share", subFeatures: "Empty state", section: "Fund page" }))).toEqual([
			["Feature", "Share > Empty state"],
			["Section", "Fund page"],
		]);
	});

	it("leaves out what the case does not set", () => {
		expect(caseFacts(detail())).toEqual([]);
		expect(caseFacts(detail({ subFeatures: "Empty state" }))).toEqual([["Feature", "Empty state"]]);
	});
});

describe("unlinkedTaskIssue", () => {
	const linkedTo = (...keys: string[]) =>
		detail({ requirements: keys.map((key) => ({ key, summary: `summary of ${key}`, status: "In Progress" })) });

	it("names the task's Jira issue while the case is not linked to it", () => {
		expect(unlinkedTaskIssue(linkedTo(), "jira:MOBILITY-4839")).toBe("MOBILITY-4839");
		expect(unlinkedTaskIssue(linkedTo("MOBILITY-4166"), "jira:MOBILITY-4839")).toBe("MOBILITY-4839");
	});

	it("is null once the case is linked to it, whatever the key's case", () => {
		expect(unlinkedTaskIssue(linkedTo("MOBILITY-4166", "MOBILITY-4839"), "jira:MOBILITY-4839")).toBeNull();
		expect(unlinkedTaskIssue(linkedTo("mobility-4839"), "jira:MOBILITY-4839")).toBeNull();
	});

	it("is null when the task has no Jira issue", () => {
		expect(unlinkedTaskIssue(linkedTo(), undefined)).toBeNull();
		expect(unlinkedTaskIssue(linkedTo(), "gh:123")).toBeNull();
	});
});

describe("textBlocks", () => {
	it("has no blocks for no text", () => {
		expect(textBlocks("")).toEqual([]);
		expect(textBlocks(" \n ")).toEqual([]);
	});

	it("keeps plain lines as one block of text", () => {
		expect(textBlocks("Logged in\nOn the fund page")).toEqual([{ kind: "text", text: "Logged in\nOn the fund page" }]);
	});

	it("reads bullet and numbered lines as lists, with the text around them", () => {
		expect(textBlocks("Before you start:\n- Logged in\n- Nothing shared yet\n\n3. Open\n4. Share\nDone")).toEqual([
			{ kind: "text", text: "Before you start:" },
			{ kind: "list", ordered: false, start: 1, items: ["Logged in", "Nothing shared yet"] },
			{ kind: "list", ordered: true, start: 3, items: ["Open", "Share"] },
			{ kind: "text", text: "Done" },
		]);
	});

	it("keeps nested or wrapped lists as written, since a flat list would lose their shape", () => {
		const nested = "- iOS\n  - iPhone\n- Android";
		expect(textBlocks(nested)).toEqual([{ kind: "text", text: nested }]);
		const table = "User | Password\nqa | fake";
		expect(textBlocks(table)).toEqual([{ kind: "text", text: table }]);
	});
});
