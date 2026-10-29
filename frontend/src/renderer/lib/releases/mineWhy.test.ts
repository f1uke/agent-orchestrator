import { describe, expect, it } from "vitest";
import { subjectsBoard } from "./fixture";
import { alertCopy, mineWhy, onlyMeSubject } from "./mineWhy";
import type { AlertHit, MineReason, Subject } from "./types";
import type { Who } from "./who";

const ME: Who = { self: true, name: "Ada Example" };
const BEN: Who = { self: false, name: "Ben Sample" };

const hit = (rule: string, subject: Subject, mine?: MineReason[]): AlertHit => ({
	id: `${rule}~x`,
	rule,
	subject,
	severity: "risk",
	bucket: "badge",
	copy: { what: "", do: "" },
	cards: [],
	evidence: {},
	links: [],
	asOf: "2026-10-19T02:00:00Z",
	...(mine ? { mine } : {}),
});

const version: Subject = { kind: "version", train: "21-2026", app: "di" };

describe("mineWhy", () => {
	const hotfixTrain: Subject = { kind: "hotfixTrain", app: "da", version: "2.3.1", train: "21-2026" };
	const hotfix: Subject = { kind: "hotfix", app: "da", version: "2.3.1" };
	const mr: Subject = { kind: "mr", app: "di", iid: 12, source: "feat/A-1", target: "x" };
	const pageFamily: Subject = {
		kind: "pageFamily",
		train: "21-2026",
		family: "demo",
		bucket: "open",
		keys: ["A-1", "A-2", "A-3", "A-4"],
	};
	const card = (key: string): Subject => ({ kind: "card", key, board: "A" });

	it("names the release MR or the release ticket for an owner rule", () => {
		const mrHit = hit("C1", version, [{ why: "release_mr_author", ref: "!77" }]);
		expect(mineWhy(mrHit, ME)).toBe("your release MR !77");
		expect(mineWhy(mrHit, BEN)).toBe("Ben Sample's release MR !77");
		const ticket = hit("D2", version, [{ why: "release_ticket_assignee", ref: "DEMO-9" }]);
		expect(mineWhy(ticket, ME)).toBe("your release ticket");
		expect(mineWhy(ticket, BEN)).toBe("Ben Sample's release ticket");
	});

	it("says the release MR before the release ticket, whatever the wire order", () => {
		const both = hit("B1", version, [
			{ why: "release_ticket_assignee", ref: "DEMO-9" },
			{ why: "release_mr_author", ref: "!77" },
		]);
		expect(mineWhy(both, ME)).toBe("your release MR !77 · your release ticket");
		expect(mineWhy(both, BEN)).toBe("Ben Sample's release MR !77 · Ben Sample's release ticket");
	});

	it("says only that the viewer has not approved for D3", () => {
		const d3 = hit("D3", version, [{ why: "approver" }]);
		expect(mineWhy(d3, ME)).toBe("you have not approved");
		expect(mineWhy(d3, BEN)).toBe("Ben Sample has not approved");
	});

	it("names only the viewer's cards, once each, for a page family", () => {
		const g5 = hit("G5", pageFamily, [
			{ why: "card", ref: "A-2" },
			{ why: "card", ref: "A-4" },
			{ why: "card", ref: "A-2" },
		]);
		expect(mineWhy(g5, ME)).toBe("your cards A-2, A-4");
		expect(mineWhy(g5, BEN)).toBe("Ben Sample's cards A-2, A-4");
	});

	it("names one card in the singular", () => {
		const g2 = hit("G2", card("A-1"), [{ why: "card", ref: "A-1" }]);
		expect(mineWhy(g2, ME)).toBe("your card A-1");
		expect(mineWhy(g2, BEN)).toBe("Ben Sample's card A-1");
	});

	it("names the MR on an MR alert and the hotfix MR on a hotfix alert", () => {
		const onMr = hit("L9", mr, [{ why: "mr_author", ref: "!12" }]);
		expect(mineWhy(onMr, ME)).toBe("your MR !12");
		expect(mineWhy(onMr, BEN)).toBe("Ben Sample's MR !12");
		const onTrain = hit("F2", hotfixTrain, [{ why: "mr_author", ref: "!88" }]);
		expect(mineWhy(onTrain, ME)).toBe("your hotfix MR !88");
		expect(mineWhy(onTrain, BEN)).toBe("Ben Sample's hotfix MR !88");
		const two = hit("E1", hotfix, [
			{ why: "mr_author", ref: "!88" },
			{ why: "mr_author", ref: "!89" },
		]);
		expect(mineWhy(two, ME)).toBe("your hotfix MRs !88, !89");
		expect(mineWhy(two, BEN)).toBe("Ben Sample's hotfix MRs !88, !89");
	});

	it("never counts a No MR needed mark", () => {
		expect(mineWhy(hit("G2", card("A-1"), [{ why: "no_mr" }]), ME)).toBe("");
	});

	it("is empty when the hit is not the viewer's", () => {
		expect(mineWhy(hit("A1", version), ME)).toBe("");
	});

	it("never says you or your for a named viewer", () => {
		const all = hit("X1", version, [
			{ why: "release_mr_author", ref: "!77" },
			{ why: "release_ticket_assignee", ref: "DEMO-9" },
			{ why: "approver" },
			{ why: "card", ref: "A-1" },
			{ why: "mr_author", ref: "!12" },
		]);
		expect(mineWhy(all, BEN)).not.toMatch(/\byou(r)?\b/i);
	});
});

describe("onlyMeSubject", () => {
	const g5: Subject = {
		kind: "pageFamily",
		train: "21-2026",
		family: "demo",
		bucket: "open",
		keys: ["A-1", "A-2", "A-3"],
	};

	it("narrows a page family to my cards under Only me", () => {
		const h = hit("G5", g5, [{ why: "card", ref: "A-3" }]);
		expect(onlyMeSubject(h, true)).toEqual({ ...g5, keys: ["A-3"] });
	});

	it("leaves the subject whole with Only me off, or with no card of mine", () => {
		expect(onlyMeSubject(hit("G5", g5, [{ why: "card", ref: "A-3" }]), false)).toBe(g5);
		expect(onlyMeSubject(hit("G5", g5), true)).toBe(g5);
		expect(onlyMeSubject(hit("C1", version, [{ why: "release_mr_author", ref: "!7" }]), true)).toBe(version);
	});
});

describe("alertCopy", () => {
	const d3 = {
		...hit("D3", version, [{ why: "approver" }]),
		copy: {
			what: "2 of 3 approvers have not approved train 22",
			do: "Ask Ann, Bob to approve before publish Tue 3 Nov.",
		},
		due: { on: "2026-11-03", event: "publish" as const, workDays: 4 },
	};

	it("tells the approver on D3 that they have not approved, without the other approvers' names", () => {
		expect(alertCopy(d3, true, "#22")).toEqual({
			what: "You have not approved train 22",
			do: "Approve the Jira release before publish Tue 3 Nov.",
		});
	});

	it("drops the date when the hit has no deadline, and the train when the board has no label", () => {
		expect(alertCopy({ ...d3, due: undefined }, true, "#22").do).toBe("Approve the Jira release before publish.");
		expect(alertCopy(d3, true).what).toBe("You have not approved this train");
	});

	it("keeps the rule's copy with Only me off and on every other rule", () => {
		expect(alertCopy(d3, false, "#22")).toBe(d3.copy);
		const c1 = {
			...hit("C1", version),
			copy: { what: "Train 22 release pipeline failed: build", do: "Retry the job." },
		};
		expect(alertCopy(c1, true, "#22")).toBe(c1.copy);
	});
});

describe("the subjects board under Only me", () => {
	it("lists exactly the alerts the mine policies allow, each with its why phrase", () => {
		const b = subjectsBoard("2026-10-19");
		const mine = b.alerts.hits.filter((h) => h.bucket === "badge" && h.mine?.length);
		expect(Object.fromEntries(mine.map((h) => [h.id, mineWhy(h, ME)]))).toEqual({
			"C5~v/21-2026/di": "your release MR !77",
			"C1~v/21-2026/di": "your release MR !77",
			"D3~v/21-2026/da": "you have not approved",
			"E1~h/da/2.3.1": "your hotfix MR !88",
			"F2~ht/da/2.3.1/21-2026": "your hotfix MR !88",
			"G5~pf/21-2026/demo/open": "your cards DEMO-104, DEMO-106",
		});
	});
});
