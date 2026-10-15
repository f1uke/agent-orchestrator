import { describe, expect, it } from "vitest";
import {
	ageText,
	badgeLabel,
	focusTrain,
	lensBadge,
	makeLens,
	nowStage,
	owner,
	sourceAges,
	trainChips,
	trainApprovers,
	unknownText,
	visibleApps,
	worstTone,
} from "./lens";
import { board, stage, train, version } from "./testkit";
import type { AlertHit, Badge, RuleOutcome } from "./types";
import { parseReleasesSearch } from "./url";

const hit = (id: string, over: Partial<AlertHit> = {}): AlertHit => ({
	id,
	rule: id.split("~")[0],
	severity: "risk",
	bucket: "badge",
	text: id,
	evidence: {},
	link: { label: "Open" },
	asOf: "2026-10-14T03:00:00Z",
	...over,
});
const unknownOutcome = (rule: string, app?: string): RuleOutcome => ({
	rule,
	app,
	on: true,
	outcome: { kind: "unknown", missing: [] },
});

describe("badgeLabel", () => {
	it.each<[Badge, string]>([
		[{ count: 12, unknown: 3, worst: "block" }, "12 · 3 unknown"],
		[{ count: 0, unknown: 3 }, "? 3 unknown"],
		[{ count: 4, unknown: 0, worst: "risk" }, "4"],
		[{ count: 0, unknown: 0 }, "0"],
	])("%j reads %s", (b, want) => {
		expect(badgeLabel(b)).toBe(want);
	});
});

describe("lens", () => {
	const t20 = train("20-2026", "2026-09-29", "2026-10-06", "past", [
		version("20-2026", "di", "2.3.0", "2026-09-29", "2026-10-06"),
	]);
	const t21 = train("21-2026", "2026-10-13", "2026-10-20", "current", [
		version("21-2026", "di", "2.4.0", "2026-10-13", "2026-10-20"),
	]);
	const b = board({
		trains: [t20, t21],
		alerts: {
			badge: { count: 3, unknown: 2, worst: "block" },
			hits: [
				hit("B1~v/21-2026/di", { app: "di", train: "21-2026", severity: "block" }),
				hit("B1~v/21-2026/da", { app: "da", train: "21-2026" }),
				hit("F1~t/21-2026", { train: "21-2026", severity: "info" }),
				hit("C1~v/20-2026/di", { app: "di", train: "20-2026", bucket: "history" }),
				hit("C2~v/21-2026/di", { app: "di", train: "21-2026", bucket: "hidden" }),
			],
			rules: [
				unknownOutcome("A6"),
				unknownOutcome("C5", "da"),
				unknownOutcome("C6", "di"),
				{ ...unknownOutcome("C7", "di"), on: false },
			],
			staleHides: [],
		},
	});

	it("focuses the current train unless the URL names one the Board has", () => {
		expect(focusTrain(b, null)?.id).toBe("21-2026");
		expect(focusTrain(b, "20-2026")?.id).toBe("20-2026");
		expect(focusTrain(b, "99-2026")?.id).toBe("21-2026");
		expect(focusTrain(board({ trains: [t20] }), null)?.id).toBe("20-2026");
	});

	it("shows every app until the filter names some, in Board order", () => {
		expect(visibleApps(b, null).map((a) => a.id)).toEqual(["di", "da"]);
		expect(visibleApps(b, new Set(["da", "zz"])).map((a) => a.id)).toEqual(["da"]);
	});

	it("counts the Board's badge with every app shown", () => {
		expect(lensBadge(b, null)).toEqual({ count: 3, unknown: 2, worst: "block" });
	});

	it("counts only badge hits of the shown apps (and app-less hits) when the filter narrows", () => {
		expect(lensBadge(b, new Set(["da"]))).toEqual({ count: 2, unknown: 2, worst: "risk" });
	});

	it("puts per-app alerts on the label, worst first, from the badge bucket only", () => {
		const lens = makeLens(b, parseReleasesSearch({}));
		expect(lens.appHits("di").map((h) => h.id)).toEqual(["B1~v/21-2026/di"]);
		expect(lens.attention.map((h) => h.id)).toEqual(["B1~v/21-2026/di", "B1~v/21-2026/da", "F1~t/21-2026"]);
		expect(lens.history.map((h) => h.id)).toEqual(["C1~v/20-2026/di"]);
		expect(lens.hidden.map((h) => h.id)).toEqual(["C2~v/21-2026/di"]);
	});
});

describe("owner", () => {
	const v = version("21-2026", "di", "2.4.0", "2026-10-13", "2026-10-20");
	it("is the release MR author, then the release ticket assignee", () => {
		const ticket = {
			key: "DEMO-9",
			summary: "Release",
			status: "To Do",
			statusCategory: "new",
			subtask: false,
			releaseTicket: true,
			fixVersions: [],
			url: "",
			assignee: { accountId: "acc-b", name: "Bee Example" },
		};
		expect(owner({ ...v, ticket })).toEqual({ name: "Bee Example", from: "release ticket assignee" });
		const releaseMr = {
			mr: {
				iid: 1,
				project: 1,
				title: "",
				state: "opened",
				author: "Ann Example",
				sourceBranch: "",
				targetBranch: "main",
				createdOn: "2026-10-13",
				url: "",
			},
		};
		expect(owner({ ...v, ticket, releaseMr })).toEqual({ name: "Ann Example", from: "release MR author" });
		expect(owner(v)).toBeNull();
	});
});

describe("trainApprovers", () => {
	it("merges approvers across apps, pending first, then by name", () => {
		const jira = (approvers: { accountId: string; name: string; status: string }[]) => ({
			id: "1",
			name: "",
			description: "",
			released: false,
			url: "",
			approvers,
		});
		const t = train("21-2026", "2026-10-13", "2026-10-20", "current", [
			version("21-2026", "di", "2.4.0", "2026-10-13", "2026-10-20", {
				jira: jira([
					{ accountId: "a", name: "Ann", status: "APPROVED" },
					{ accountId: "c", name: "Cee", status: "PENDING" },
				]),
			}),
			version("21-2026", "da", "2.4.0", "2026-10-13", "2026-10-20", {
				jira: jira([
					{ accountId: "a", name: "Ann", status: "PENDING" },
					{ accountId: "b", name: "Bee", status: "APPROVED" },
				]),
			}),
		]);
		const people = trainApprovers(t, ["di", "da"]);
		expect(people.map((p) => [p.accountId, p.pending])).toEqual([
			["a", true],
			["c", true],
			["b", false],
		]);
		expect(people[0].apps).toEqual([
			{ app: "di", approved: true },
			{ app: "da", approved: false },
		]);
		expect(trainApprovers(t, ["da"]).map((p) => p.accountId)).toEqual(["a", "b"]);
	});
});

describe("stage summaries", () => {
	it("names the first stage not done, skipping one no system records", () => {
		const stages = [
			stage("freeze", "2026-10-13", "done"),
			stage("force", undefined, "not_observable"),
			stage("scan", "2026-10-14", "unknown"),
		];
		expect(nowStage(stages)?.key).toBe("scan");
		expect(nowStage([stage("publish", "2026-10-20", "done")])).toBeNull();
	});

	it("paints a bar with its worst stage: failed over unknown over waiting over running", () => {
		expect(worstTone([stage("scan", "x", "running"), stage("approve", "x", "waiting")])).toBe("waiting");
		expect(worstTone([stage("scan", "x", "unknown"), stage("approve", "x", "waiting")])).toBe("unknown");
		expect(worstTone([stage("scan", "x", "unknown"), stage("tag", "x", "failed")])).toBe("failed");
		expect(worstTone([stage("scan", "x", "done"), stage("force", undefined, "not_observable")])).toBe("done");
	});

	it("says how long ago an unknown read last completed, or that it never did", () => {
		const now = Date.parse("2026-10-14T03:10:00Z");
		expect(
			unknownText(
				[
					{
						kind: "gitlab.mrs",
						scope: "p",
						failures: 2,
						errorClass: "unavailable",
						completeAt: "2026-10-14T02:10:00Z",
					},
				],
				now,
			),
		).toBe("unknown: gitlab.mrs unavailable, last read 1h ago");
		expect(unknownText([{ kind: "jira.versions", scope: "p", failures: 0 }], now)).toBe(
			"unknown: jira.versions never read",
		);
		expect(unknownText(undefined, now)).toBe("unknown");
	});
});

describe("source ages", () => {
	const now = Date.parse("2026-10-14T03:10:00Z");
	const sources = [
		{ system: "gitlab", resources: 4, asOf: "2026-10-14T03:08:00Z", issues: [] },
		{
			system: "jira",
			resources: 3,
			asOf: "2026-10-14T02:10:00Z",
			issues: [{ kind: "jira.versions", scope: "p", failures: 3, errorClass: "unavailable" as const }],
		},
		{ system: "confluence", resources: 1, issues: [] },
	];

	it("says each source's age, marks one with issues or never read as unknown", () => {
		expect(sourceAges(sources, now, null)).toEqual([
			{ system: "gitlab", age: "2m", unknown: false, why: "" },
			{ system: "jira", age: "1h", unknown: true, why: "1 of 3 not fresh: jira.versions unavailable" },
			{ system: "confluence", age: "never read", unknown: true, why: "never read" },
		]);
	});

	it("marks every source unknown with its last age when the Board itself cannot be fetched", () => {
		const ages = sourceAges(sources, now, "daemon unreachable");
		expect(ages.every((s) => s.unknown)).toBe(true);
		expect(ages[0]).toEqual({ system: "gitlab", age: "2m", unknown: true, why: "daemon unreachable" });
	});

	it("rounds ages the way ao train does", () => {
		expect(ageText(30_000)).toBe("now");
		expect(ageText(5 * 60_000)).toBe("5m");
		expect(ageText(3 * 3_600_000)).toBe("3h");
		expect(ageText(50 * 3_600_000)).toBe("2d");
	});
});

describe("train chips", () => {
	const t = (n: number, phase: "past" | "current" | "next") =>
		train(`${n}-2026`, "2026-01-01", "2026-01-08", phase, [
			version(`${n}-2026`, "di", `1.${n}.0`, "2026-01-01", "2026-01-08"),
		]);
	const b = board({ trains: [t(18, "past"), t(19, "past"), t(20, "past"), t(21, "current"), t(22, "next")] });

	it("shows the live trains and the last published one", () => {
		expect(trainChips(b, false, null).map((x) => x.id)).toEqual(["20-2026", "21-2026", "22-2026"]);
	});

	it("shows every train with the History layer on", () => {
		expect(trainChips(b, true, null).map((x) => x.id)).toEqual(["18-2026", "19-2026", "20-2026", "21-2026", "22-2026"]);
	});

	it("keeps an older train the URL focuses", () => {
		expect(trainChips(b, false, "18-2026").map((x) => x.id)).toEqual(["18-2026", "20-2026", "21-2026", "22-2026"]);
	});
});
