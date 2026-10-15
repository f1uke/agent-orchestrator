import { describe, expect, it } from "vitest";
import { boardLabel, destKey, destLabel, destTone, mineText, mrPills } from "./sprint";
import { board, train, version } from "./testkit";
import type { SprintCard, SprintMR, SprintRow } from "./types";

const mr = (app: string, iid: number, dest: SprintMR["dest"]): SprintMR => ({
	app,
	iid,
	dest,
	url: `https://gitlab.example.com/mr/${iid}`,
	title: `MR ${iid}`,
	state: dest.endsWith("_open") ? "opened" : "merged",
	source: `feat/${iid}`,
	target: "sprint-20-2026",
	author: "Kai",
	created: "2026-10-01",
});

const card = (key: string, mrs: SprintMR[] = []): SprintCard => ({
	key,
	url: `https://jira.example.com/browse/${key}`,
	summary: key,
	status: "In Progress",
	statusCategory: "indeterminate",
	subtasks: [],
	mrs,
	routes: [],
	onBoard: true,
	active: true,
	activeWhy: ["new this sprint"],
});

const t21 = train("21-2026", "2026-10-13", "2026-10-20", "current", [
	version("21-2026", "di", "2.4.0", "2026-10-13", "2026-10-20"),
]);
const b = board({
	apps: [
		{ id: "di", name: "Demo iOS", family: "demo", platform: "ios" },
		{ id: "da", name: "Demo Android", family: "demo", platform: "android" },
		{ id: "pi", name: "Pilot iOS", family: "pilot", platform: "ios" },
	],
	trains: [t21],
	llbs: [{ id: "demo-wallet", name: "Wallet", branches: [], target: { kind: "planned", train: "22-2026" } }],
});

const row: SprintRow = {
	board: { id: 1, key: "DEMO", ownsSprintBranch: true },
	number: 20,
	sprint: { board: 1, id: 20, name: "DEMO 20", url: "", start: "2026-10-06", end: "2026-10-20" },
	total: 30,
	cards: [],
	counts: { new: 21, carriedWithMrs: 6, carriedDone: 7 },
	dests: [
		{ to: { kind: "train", train: "21-2026" }, count: 12, keys: [] },
		{ to: { kind: "llb", group: "demo-wallet", branch: "feature/demo-wallet", via: "22-2026" }, count: 2, keys: [] },
		{ to: { kind: "off" }, count: 1, keys: [] },
		{ to: { kind: "none" }, count: 4, keys: [] },
	],
};

describe("board sprint label", () => {
	it("reads three lines: board and dates, new and carried counts, destination counts in their colours", () => {
		expect(boardLabel(row, b)).toEqual({
			title: "DEMO 20",
			dates: "6 Oct → 20 Oct",
			scope: "21 new · 6 carried with MRs · 7 carried, done",
			dests: [
				{ text: "#21 12", tone: "accent" },
				{ text: "⑂ 2", tone: "hotfix" },
				{ text: "off 1", tone: "failed" },
				{ text: "no MR 4", tone: "todo" },
			],
		});
	});

	it("leaves out a zero carried count", () => {
		expect(boardLabel({ ...row, counts: { new: 3, carriedWithMrs: 0, carriedDone: 0 } }, b).scope).toBe("3 new");
	});
});

describe("destinations", () => {
	it("names each destination kind for a heading", () => {
		expect(destLabel(b, { kind: "train", train: "21-2026" })).toBe("→ #21");
		expect(destLabel(b, { kind: "train", train: "23-2026" })).toBe("→ #23");
		expect(destLabel(b, { kind: "llb", group: "demo-wallet", branch: "feature/demo-wallet" })).toBe("⑂ demo-wallet");
		expect(destLabel(b, { kind: "before", train: "21-2026" })).toBe("→ before #21");
		expect(destLabel(b, { kind: "off" })).toBe("! off the sprint flow");
		expect(destLabel(b, { kind: "none" })).toBe("· not routed yet");
	});

	it("keys and paints a destination the same way everywhere", () => {
		expect(destKey({ kind: "train", train: "21-2026" })).toBe("t:21-2026");
		expect(destKey({ kind: "llb", group: "g", branch: "feature/g" })).toBe("l:g");
		expect(destTone(b, { kind: "train", train: "22-2026" })).toBe("todo");
		expect(destTone(b, { kind: "before", train: "21-2026" })).toBe("done");
	});
});

describe("MR family pills", () => {
	it("makes one pill per family with an MR, one glyph per app coloured by its destination, the rest blank", () => {
		const c = card("DEMO-1", [mr("di", 1, "sprint"), mr("di", 2, "sprint_open"), mr("da", 3, "develop")]);
		expect(mrPills(c, b.apps)).toEqual([
			{
				family: "demo",
				label: "Dem",
				apps: [
					{ app: "di", platform: "ios", kind: "sprint", tone: "done", mrs: [1, 2] },
					{ app: "da", platform: "android", kind: "develop", tone: "failed", mrs: [3] },
				],
			},
			{ family: "pilot", label: "Pil", apps: [] },
		]);
	});

	it("colours an app by its merged MR before an open one", () => {
		const c = card("DEMO-2", [mr("pi", 5, "llb_open"), mr("pi", 6, "llb")]);
		expect(mrPills(c, b.apps)[1].apps[0]).toMatchObject({ kind: "llb", tone: "hotfix" });
	});
});

describe("mineText", () => {
	it("says why something is mine in a few words", () => {
		expect(mineText([{ why: "assignee" }, { why: "mr_author", ref: "!12" }, { why: "subtask", ref: "DEMO-9" }])).toBe(
			"assignee · your MR !12 · subtask DEMO-9",
		);
	});
});
