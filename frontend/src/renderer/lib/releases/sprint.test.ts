import { describe, expect, it } from "vitest";
import {
	boardLabel,
	carriedTo,
	destBlocks,
	destHead,
	destKey,
	destLabel,
	destTally,
	destTone,
	mineText,
	mrPills,
	shortAppName,
	sprintCount,
	sprintShipsIn,
} from "./sprint";
import { board, train, version } from "./testkit";
import type { Route, SprintCard, SprintMR, SprintRow } from "./types";

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

const card = (key: string, mrs: SprintMR[] = [], over: Partial<SprintCard> = {}): SprintCard => ({
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
	...over,
});

const route = (app: string, kind: Route["kind"], to: Route["to"], mr = 0, over: Partial<Route> = {}): Route => ({
	app,
	kind,
	to,
	mr,
	...over,
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

const t21Dest = { kind: "train", train: "21-2026" } as const;
const wallet = { kind: "llb", group: "demo-wallet", branch: "feature/demo-wallet", via: "22-2026" } as const;
const cards: SprintCard[] = [
	card("DEMO-1", [], { routes: [route("di", "sprint", t21Dest)] }),
	card("DEMO-2", [], { routes: [route("di", "sprint", t21Dest), route("da", "llb", wallet)] }),
	card("DEMO-3", [], { activeWhy: ["MR this sprint"], routes: [route("di", "develop", { kind: "off" })] }),
	card("DEMO-4", [], { activeWhy: ["mobile subtask open"] }),
	card("DEMO-11", [], { active: false, activeWhy: [], routes: [route("di", "sprint", t21Dest)] }),
	card("DEMO-9", [], { active: false, activeWhy: [], routes: [route("da", "llb", wallet)] }),
];
const fullRow: SprintRow = {
	...row,
	cards,
	dests: [
		{ to: t21Dest, count: 2, keys: ["DEMO-1", "DEMO-2"] },
		{ to: wallet, count: 1, keys: ["DEMO-2"] },
		{ to: { kind: "off" }, count: 1, keys: ["DEMO-3"] },
		{ to: { kind: "none" }, count: 1, keys: ["DEMO-4"] },
	],
};

describe("board sprint label", () => {
	it("reads three lines: board and dates, new and carried counts, destination counts in their colours", () => {
		expect(boardLabel(fullRow, b, cards)).toEqual({
			title: "DEMO 20",
			dates: "6 Oct → 20 Oct",
			scope: "2 new · 2 carried with MRs · 2 carried, done",
			dests: [
				{ text: "#21 2", tone: "accent" },
				{ text: "⑂ 1", tone: "hotfix" },
				{ text: "off 1", tone: "failed" },
				{ text: "no MR 1", tone: "todo" },
			],
			routes: [
				{ text: "→ #21 2", tone: "accent" },
				{ text: "⑂ demo-wallet 1", tone: "hotfix" },
				{ text: "off flow 1", tone: "failed" },
				{ text: "no MR 1", tone: "todo" },
				{ text: "+2 carried over", tone: "todo" },
			],
		});
	});

	it("counts only the cards the lens keeps, so Only me and the app filter change every number", () => {
		const kept = cards.filter((c) => ["DEMO-2", "DEMO-9"].includes(c.key));
		const l = boardLabel(fullRow, b, kept);
		expect(l.scope).toBe("1 new · 1 carried, done");
		expect(l.dests).toEqual([
			{ text: "#21 1", tone: "accent" },
			{ text: "⑂ 1", tone: "hotfix" },
		]);
	});

	it("leaves out a zero carried count", () => {
		expect(boardLabel(fullRow, b, cards.slice(0, 2)).scope).toBe("2 new");
	});
});

describe("destination blocks", () => {
	it("keeps the server's destination order, drops destinations with no kept card, and puts carried cards last", () => {
		const blocks = destBlocks(
			fullRow,
			cards.filter((c) => c.key !== "DEMO-3"),
		);
		expect(blocks.map((x) => [x.key, x.cards.map((c) => c.key)])).toEqual([
			["t:21-2026", ["DEMO-1", "DEMO-2"]],
			["l:demo-wallet", ["DEMO-2"]],
			["none", ["DEMO-4"]],
			["carried", ["DEMO-9", "DEMO-11"]],
		]);
	});

	it("heads a train block with its freeze and versions, a long-lived block with its target, the rest with what they mean", () => {
		const b2 = { ...b, llbs: [{ ...b.llbs[0], target: { kind: "landed" as const, train: "21-2026" } }] };
		expect(destHead(b, t21Dest, cards.slice(0, 2))).toEqual({
			to: "→ #21",
			tone: "accent",
			freeze: "2026-10-13",
			note: "2.4.0",
		});
		expect(destHead(b, wallet, [cards[1]])).toMatchObject({ to: "⑂ demo-wallet", target: { text: "then → #22" } });
		expect(destHead(b2, wallet, [cards[1]]).target).toEqual({ text: "landed in #21", tone: "done" });
		expect(destHead(b, { kind: "off" }, []).note).toBe("straight into develop or another branch");
		expect(destHead(b, { kind: "none" }, []).note).toBe("no MR in any repo");
		expect(destHead(b, null, cards.slice(4)).note).toBe(
			"in an earlier sprint, mobile work done before this one · 0 ready or deployed in Jira",
		);
	});

	it("tallies the routes into a block: merged, in review, split across destinations, not as planned", () => {
		const list = [
			card("DEMO-1", [], { routes: [route("di", "sprint", t21Dest), route("da", "sprint_open", t21Dest)] }),
			card("DEMO-2", [], {
				routes: [route("di", "sprint", t21Dest, 0, { mismatch: true, plan: "22-2026" }), route("da", "llb", wallet)],
			}),
		];
		expect(destTally(t21Dest, list)).toBe("2 MR merged · 1 in review · 1 split · 1 not as planned");
		expect(destTally(wallet, [list[1]])).toBe("1 split");
	});

	it("says where a carried card went", () => {
		expect(carriedTo(b, cards[4])).toEqual({ text: "went to #21", tone: "passive" });
		expect(carriedTo(b, cards[5])).toEqual({ text: "parked in ⑂ demo-wallet", tone: "hotfix" });
		expect(carriedTo(b, card("DEMO-12", [], { active: false }))).toBeNull();
	});
});

describe("sprint numbers", () => {
	const t22 = train("22-2026", "2026-10-27", "2026-11-03", "next", []);
	const t21x = train("21-1-2026", "2026-10-15", "2026-10-22", "next", []);
	const bb = board({ trains: [t21x, t22, t21], sprints: [fullRow] });

	it("ships the owning board's sprint N in train N+1, never in an extra train such as #21-1", () => {
		expect(sprintShipsIn(bb, { ...fullRow, number: 20 })?.id).toBe("21-2026");
		expect(sprintShipsIn(bb, { ...fullRow, number: 30 })).toBeUndefined();
	});

	it("counts the active, not abandoned sprint cards of every board", () => {
		const abandoned = card("DEMO-13", [], { status: "Abandoned" });
		expect(sprintCount(board({ sprints: [{ ...fullRow, cards: [...cards, abandoned] }, fullRow] }))).toBe(8);
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
					{
						app: "di",
						platform: "ios",
						kind: "sprint",
						tone: "done",
						mrs: [1, 2],
						url: "https://gitlab.example.com/mr/1",
					},
					{
						app: "da",
						platform: "android",
						kind: "develop",
						tone: "failed",
						mrs: [3],
						url: "https://gitlab.example.com/mr/3",
					},
				],
			},
			{ family: "pilot", label: "Pil", apps: [] },
		]);
	});

	it("labels a pill with the short family name the top bar's app chips use", () => {
		const apps = [{ id: "xi", name: "F4A iOS", family: "f4a", platform: "ios" as const }];
		expect(mrPills(card("DEMO-4"), apps)[0].label).toBe("F4A");
		expect(shortAppName(apps[0])).toBe("F4A iOS");
	});

	it("colours an app by its merged MR before an open one", () => {
		const c = card("DEMO-2", [mr("pi", 5, "llb_open"), mr("pi", 6, "llb")]);
		expect(mrPills(c, b.apps)[1].apps[0]).toMatchObject({ kind: "llb", tone: "hotfix" });
	});

	it("takes the colour and the link from the app's route, the same one the row's lead glyph reads", () => {
		const c = card("DEMO-3", [mr("di", 7, "sprint"), mr("di", 8, "develop_open")], {
			routes: [route("di", "develop_open", { kind: "off" }, 8)],
		});
		expect(mrPills(c, b.apps)[0].apps[0]).toMatchObject({
			kind: "develop_open",
			tone: "failed",
			mrs: [7, 8],
			url: "https://gitlab.example.com/mr/8",
		});
	});
});

describe("mineText", () => {
	it("says why something is mine in a few words", () => {
		expect(mineText([{ why: "assignee" }, { why: "mr_author", ref: "!12" }, { why: "subtask", ref: "DEMO-9" }])).toBe(
			"assignee · your MR !12 · subtask DEMO-9",
		);
	});
});
