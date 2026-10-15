import { describe, expect, it } from "vitest";
import { cardGroups, groupRowCount, statusTone } from "./cards";
import { board, train, version } from "./testkit";
import type { CardUnit, LLBGroup, LongLivedBranch } from "./types";

const unit = (
	key: string,
	code: CardUnit["code"],
	status = "In Progress",
	statusCategory = "indeterminate",
): CardUnit => ({
	key,
	url: `https://jira.example.com/browse/${key}`,
	summary: `Card ${key}`,
	app: "di",
	status,
	statusCategory,
	why: ["fixVersion"],
	code,
});

const branch = (name: string, over: Partial<LongLivedBranch> = {}): LongLivedBranch => ({
	app: "di",
	branch: name,
	openedOn: "2026-09-01",
	lastIn: "2026-10-01",
	state: "open",
	cards: [],
	...over,
});

const llbCard = (key: string): LongLivedBranch["cards"][number] => ({
	key,
	summary: `Card ${key}`,
	status: "Done",
	statusCategory: "done",
	mr: {
		iid: 1,
		project: 1,
		title: key,
		state: "merged",
		author: "Kai",
		sourceBranch: `feat/${key}`,
		targetBranch: "feature/demo-wallet",
		createdOn: "2026-09-02",
		mergedOn: "2026-09-03",
		url: "",
	},
});

describe("card groups", () => {
	const t20 = train("20-2026", "2026-09-29", "2026-10-06", "past", [
		version("20-2026", "di", "2.3.0", "2026-09-29", "2026-10-06", {
			cards: [unit("DEMO-1", { kind: "merged", mrs: [1] })],
		}),
	]);
	const t21 = train("21-2026", "2026-10-13", "2026-10-20", "current", [
		version("21-2026", "di", "2.4.0", "2026-10-13", "2026-10-20", {
			cards: [
				unit("DEMO-2", { kind: "merged", mrs: [2] }, "Ready for UAT", "indeterminate"),
				unit("DEMO-3", { kind: "open", mrs: [3] }),
				unit("DEMO-4", { kind: "parked", branch: "feature/demo-wallet", mrs: [4] }),
				unit("DEMO-5", { kind: "none" }),
			],
		}),
	]);
	const t22 = train("22-2026", "2026-10-27", "2026-11-03", "next", [
		version("22-2026", "di", "2.5.0", "2026-10-27", "2026-11-03"),
	]);
	const wallet: LLBGroup = {
		id: "demo-wallet",
		name: "demo-wallet",
		branches: [branch("feature/demo-wallet", { cards: [llbCard("DEMO-9"), llbCard("DEMO-9")] })],
		target: { kind: "planned", train: "21-2026" },
	};
	const b = board({ trains: [t20, t21, t22], llbs: [wallet] });

	it("groups a live train's cards under it and skips published trains and trains with no cards", () => {
		expect(cardGroups(b, "di").map((g) => g.train.id)).toEqual(["21-2026"]);
	});

	it("tallies code states, ready cards and the long-lived branches that ride the train", () => {
		const g = cardGroups(b, "di")[0];
		expect(g.tally).toEqual({ merged: 1, open: 1, parked: 1, elsewhere: 0, none: 1 });
		expect(g.ready).toBe(1);
		expect(g.llbs.map((x) => x.branch.branch)).toEqual(["feature/demo-wallet"]);
	});

	it("nests a parked card under its long-lived branch, once per key, with the branch's own cards", () => {
		const g = cardGroups(b, "di")[0];
		expect(g.direct.map((u) => u.key)).toEqual(["DEMO-2", "DEMO-3", "DEMO-5"]);
		expect(g.llbs[0].cards.map((c) => c.key)).toEqual(["DEMO-9", "DEMO-4"]);
	});

	it("counts the rows an open group draws: direct cards, one per branch, and an open branch's cards", () => {
		const g = cardGroups(b, "di")[0];
		expect(groupRowCount(g, () => false)).toBe(3 + 1);
		expect(groupRowCount(g, (k) => k === "di:21-2026:llb:feature/demo-wallet")).toBe(3 + 1 + 2);
	});

	it("carries mine on each row, from the unit or the long-lived branch's own card", () => {
		const mine = [{ why: "assignee" as const }];
		const t = train("21-2026", "2026-10-13", "2026-10-20", "current", [
			version("21-2026", "di", "2.4.0", "2026-10-13", "2026-10-20", {
				cards: [
					{ ...unit("DEMO-2", { kind: "merged", mrs: [2] }), mine },
					unit("DEMO-3", { kind: "open", mrs: [3] }),
					{ ...unit("DEMO-4", { kind: "parked", branch: "feature/demo-wallet", mrs: [4] }), mine },
				],
			}),
		]);
		const w: LLBGroup = {
			...wallet,
			branches: [
				branch("feature/demo-wallet", {
					cards: [{ ...llbCard("DEMO-9"), mine }, llbCard("DEMO-4"), llbCard("DEMO-8")],
				}),
			],
		};
		const g = cardGroups(board({ trains: [t], llbs: [w] }), "di")[0];
		expect(g.direct.map((u) => [u.key, !!u.mine?.length])).toEqual([
			["DEMO-2", true],
			["DEMO-3", false],
		]);
		expect(g.llbs[0].cards.map((c) => [c.key, !!c.mine?.length])).toEqual([
			["DEMO-9", true],
			["DEMO-4", true],
			["DEMO-8", false],
		]);
	});

	it("does not nest a branch whose group targets another train", () => {
		const other = board({ trains: [t21], llbs: [{ ...wallet, target: { kind: "planned", train: "22-2026" } }] });
		expect(cardGroups(other, "di")[0].llbs).toEqual([]);
	});
});

describe("statusTone", () => {
	it.each([
		["Ready for UAT", "indeterminate", "done"],
		["Done", "done", "done"],
		["In Progress", "indeterminate", "running"],
		["To Do", "new", "todo"],
	] as const)("%s (%s) paints %s", (status, cat, want) => {
		expect(statusTone(status, cat)).toBe(want);
	});
});
