import { describe, expect, it } from "vitest";
import {
	ageText,
	agoText,
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
import { cardGroups } from "./cards";
import { demoBoard, unknownIdentityBoard, viewBoardAs } from "./fixture";
import { board, hotfix as hotfixOf, stage, train, version } from "./testkit";
import type { AlertHit, Badge, RuleInfo, RuleOutcome } from "./types";
import { parseReleasesSearch } from "./url";

const hit = (id: string, over: Partial<AlertHit> = {}): AlertHit => ({
	id,
	rule: id.split("~")[0],
	subject: { kind: "version", train: "21-2026", app: "di" },
	severity: "risk",
	bucket: "badge",
	text: id,
	evidence: {},
	links: [],
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
		const lens = makeLens(b, parseReleasesSearch({}), new Set());
		expect(lens.appHits("di").map((h) => h.id)).toEqual(["B1~v/21-2026/di"]);
		expect(lens.attention.map((h) => h.id)).toEqual(["B1~v/21-2026/di", "B1~v/21-2026/da", "F1~t/21-2026"]);
		expect(lens.history.map((h) => h.id)).toEqual(["C1~v/20-2026/di"]);
		expect(lens.hidden.map((h) => h.id)).toEqual(["C2~v/21-2026/di"]);
	});

	it("drops a rule's hits the moment it is turned off, before the Board refetches", () => {
		const off = { ...b, catalogue: [{ id: "B1", on: false } as RuleInfo] };
		expect(makeLens(off, parseReleasesSearch({}), new Set()).attention.map((h) => h.id)).toEqual(["F1~t/21-2026"]);
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

	it("says just now rather than now ago", () => {
		expect(agoText(30_000)).toBe("just now");
		expect(agoText(5 * 60_000)).toBe("5m ago");
	});
});

describe("Only me", () => {
	const mine = [{ why: "assignee" as const }];
	const unit = (key: string, m = false) => ({
		key,
		url: "",
		summary: key,
		app: "di",
		status: "To Do",
		statusCategory: "new",
		why: ["fixVersion" as const],
		evidence: { fixVersion: { id: "5001", name: "iOS 2.4.0", url: "" } },
		factsFrom: "jira.cards",
		code: { kind: "none" as const },
		...(m ? { mine } : {}),
	});
	const t21 = train("21-2026", "2026-10-13", "2026-10-20", "current", [
		version("21-2026", "di", "2.4.0", "2026-10-13", "2026-10-20", {
			cards: [unit("DEMO-1", true), unit("DEMO-2")],
			mine,
		}),
		version("21-2026", "da", "2.4.0", "2026-10-13", "2026-10-20", { cards: [unit("DEMO-3")] }),
	]);
	const sprintCard = (key: string, m: boolean) => ({
		key,
		url: "",
		summary: key,
		status: "To Do",
		statusCategory: "new",
		subtasks: [],
		mrs: [],
		routes: [],
		onBoard: true,
		mobile: true,
		active: true,
		activeWhy: ["new this sprint" as const],
		...(m ? { mine } : {}),
	});
	const sprintMr = (app: string) => ({
		app,
		iid: 1,
		url: "",
		title: "",
		state: "merged",
		source: "feat/x",
		target: "sprint-20-2026",
		author: "Dee Example",
		created: "2026-10-01",
		dest: "sprint" as const,
	});
	const deePerson = {
		id: "dee",
		name: "Dee Example",
		jiraAccountId: "acc-dee",
		gitlabUsername: "dee",
		match: "self" as const,
		self: true,
	};
	const b = board({
		identity: { kind: "known", jiraAccountId: "acc-dee", gitlabUsername: "dee", displayName: "Dee Example" },
		people: [deePerson],
		viewer: { kind: "person", person: deePerson },
		trains: [t21],
		hotfixes: [
			{ ...hotfixOf("di", "2.3.1", "2026-10-14", "2026-10-16"), mine },
			hotfixOf("da", "2.3.2", "2026-10-14", "2026-10-16"),
		],
		llbs: [
			{ id: "a", name: "a", branches: [], target: { kind: "none" }, mine },
			{ id: "b", name: "b", branches: [], target: { kind: "none" } },
		],
		sprints: [
			{
				board: { id: 1, key: "DEMO", ownsSprintBranch: true },
				next: false,
				estimated: false,
				number: 20,
				sprint: { board: 1, id: 20, name: "DEMO 20", url: "" },
				total: 2,
				cards: [sprintCard("DEMO-7", true), sprintCard("DEMO-8", false)],
				counts: { new: 2, carriedWithMrs: 0, carriedDone: 0 },
				dests: [],
			},
		],
		alerts: {
			badge: { count: 2, unknown: 1, worst: "block" },
			hits: [
				hit("C1~v/21-2026/di", { app: "di", train: "21-2026", severity: "block", mine }),
				hit("B1~v/21-2026/da", { app: "da", train: "21-2026" }),
			],
			rules: [unknownOutcome("C5", "da")],
			staleHides: [],
		},
	});
	const me = makeLens(b, parseReleasesSearch({ person: "dee" }), new Set());
	const all = makeLens(b, parseReleasesSearch({}), new Set());

	it("keeps only my versions, sprint cards, long-lived groups, hotfixes and hits", () => {
		expect(me.me).toBe(true);
		expect(me.keepVersion(t21.versions[0])).toBe(true);
		expect(me.keepVersion(t21.versions[1])).toBe(false);
		expect(me.sprintCards(b.sprints[0]).map((c) => c.key)).toEqual(["DEMO-7"]);
		expect(me.llbs.map((g) => g.id)).toEqual(["a"]);
		expect(b.hotfixes.filter(me.keepHotfix).map((h) => h.version)).toEqual(["2.3.1"]);
		expect(me.attention.map((h) => h.id)).toEqual(["C1~v/21-2026/di"]);
	});

	it("leaves the card lists inside a train whole, and shows my face only with Only me on", () => {
		expect(cardGroups(b, "di")[0].units.map((u) => u.key)).toEqual(["DEMO-1", "DEMO-2"]);
		expect(cardGroups(b, "da")[0].units.map((u) => u.key)).toEqual(["DEMO-3"]);
		expect(me.face?.jiraAccountId).toBe("acc-dee");
		expect(me.viewer?.name).toBe("Dee Example");
		expect(all.face).toBeNull();
	});

	it("keeps everything with Only me off", () => {
		expect(all.me).toBe(false);
		expect(all.keepVersion(t21.versions[1])).toBe(true);
		expect(all.sprintCards(b.sprints[0])).toHaveLength(2);
		expect(all.llbs).toHaveLength(2);
		expect(all.attention).toHaveLength(2);
	});

	it("counts the badge per lens: my hits only, and no unknown cell it cannot tie to me", () => {
		expect(all.badge).toEqual({ count: 2, unknown: 1, worst: "block" });
		expect(me.badge).toEqual({ count: 1, unknown: 0, worst: "block" });
	});

	it("drops a sprint card whose MRs are all in apps the filter hides, and keeps one with no MR", () => {
		const row = {
			...b.sprints[0],
			cards: [
				{ ...sprintCard("DEMO-20", false), mrs: [sprintMr("da")] },
				{ ...sprintCard("DEMO-21", false), mrs: [sprintMr("da"), sprintMr("di")] },
				sprintCard("DEMO-22", false),
			],
		};
		const onlyDi = makeLens(b, parseReleasesSearch({ apps: "di" }), new Set());
		expect(onlyDi.sprintCards(row).map((c) => c.key)).toEqual(["DEMO-21", "DEMO-22"]);
	});

	it("takes a hidden board's row and cards off the page but leaves its alerts in Needs attention", () => {
		const hid = makeLens(b, parseReleasesSearch({}), new Set(["DEMO"]));
		expect(hid.sprintRows).toEqual([]);
		expect(hid.sprintCards(b.sprints[0])).toEqual([]);
		expect(hid.attention).toEqual(all.attention);
		expect(hid.badge).toEqual(all.badge);
		expect(all.sprintRows.map((r) => r.board.key)).toEqual(["DEMO"]);
	});

	it("hides a board's next-sprint row together with its active one", () => {
		const withNext = {
			...b,
			sprints: [
				b.sprints[0],
				{
					...b.sprints[0],
					next: true,
					estimated: false,
					number: 21,
					sprint: { board: 1, id: 21, name: "DEMO 21", url: "" },
				},
			],
		};
		const shown = makeLens(withNext, parseReleasesSearch({}), new Set());
		expect(shown.sprintRows.map((r) => r.next)).toEqual([false, true]);
		const hid = makeLens(withNext, parseReleasesSearch({}), new Set(["DEMO"]));
		expect(hid.sprintRows).toEqual([]);
		expect(hid.sprintCards(withNext.sprints[1])).toEqual([]);
	});

	it("filters only once the Board carries the named person's Mine, so a Board fetched for someone else never filters wrongly", () => {
		const ann = {
			id: "ann",
			name: "Ann Example",
			jiraAccountId: "acc-ann",
			gitlabUsername: "ann",
			match: "name" as const,
		};
		const stale = makeLens(b, parseReleasesSearch({ person: "ann" }), new Set());
		expect(stale.me).toBe(false);
		expect(stale.face).toBeNull();
		expect(stale.attention).toHaveLength(2);
		expect(stale.llbs).toHaveLength(2);
		const fresh = makeLens(
			{ ...b, people: [deePerson, ann], viewer: { kind: "person", person: ann } },
			parseReleasesSearch({ person: "ann" }),
			new Set(),
		);
		expect(fresh.me).toBe(true);
		expect(fresh.viewer?.id).toBe("ann");
	});

	it("takes the person in any form the API accepts, so a link added to the people file keeps an old URL working", () => {
		const bobby = {
			id: "bobby",
			name: "Bob Builder",
			jiraAccountId: "acc-bob",
			gitlabUsername: "bobby",
			match: "linked" as const,
		};
		const withBobby = { ...b, people: [deePerson, bobby], viewer: { kind: "person" as const, person: bobby } };
		for (const id of ["bobby", "jira:acc-bob"])
			expect(makeLens(withBobby, parseReleasesSearch({ person: id }), new Set()).me).toBe(true);
	});

	it("filters nothing when the person is not on the Board, whatever the reason", () => {
		const unknown = makeLens(
			{ ...b, viewer: { kind: "unknown", id: "zed", reason: 'no person "zed" on the Board' } },
			parseReleasesSearch({ person: "zed" }),
			new Set(),
		);
		expect(unknown.me).toBe(false);
		expect(unknown.face).toBeNull();
		expect(unknown.badge.count).toBe(2);
		expect(unknown.attention).toHaveLength(2);
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

describe("whose Mine the lens words", () => {
	const own = demoBoard("2026-10-14");
	const ben = demoBoard("2026-10-14");
	viewBoardAs(ben, "bsample");

	it("says you for the monitor's own person and the name for anyone else", () => {
		expect(makeLens(own, parseReleasesSearch({ person: "ada" }), new Set()).scope).toEqual({
			self: true,
			name: "Ada Example",
		});
		expect(makeLens(ben, parseReleasesSearch({ person: "bsample" }), new Set()).scope).toEqual({
			self: false,
			name: "Ben Sample",
		});
	});

	it("has no scope while the page shows everyone, but still knows whose Mine the Board carries", () => {
		const all = makeLens(own, parseReleasesSearch({}), new Set());
		expect(all.scope).toBeNull();
		expect(all.who).toEqual({ self: true, name: "Ada Example" });
	});

	it("names even the monitor's own person in a read-only host, filtered or not", () => {
		const adaOnly = makeLens(own, parseReleasesSearch({ person: "ada" }), new Set(), true);
		expect(adaOnly.scope).toEqual({ self: false, name: "Ada Example" });
		expect(makeLens(own, parseReleasesSearch({}), new Set(), true).who).toEqual({ self: false, name: "Ada Example" });
	});

	it("has no one to word when the Board's viewer is unknown", () => {
		expect(makeLens(unknownIdentityBoard("2026-10-14"), parseReleasesSearch({}), new Set()).who).toBeNull();
	});
});
