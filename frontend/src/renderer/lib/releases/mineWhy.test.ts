import { describe, expect, it } from "vitest";
import { subjectsBoard } from "./fixture";
import { alertText, mineWhy, minePolicyOf, onlyMeSubject } from "./mineWhy";
import type { AlertHit, MineReason, RuleInfo, Subject } from "./types";

const hit = (rule: string, subject: Subject, mine?: MineReason[]): AlertHit => ({
	id: `${rule}~x`,
	rule,
	subject,
	severity: "risk",
	bucket: "badge",
	text: "",
	evidence: {},
	links: [],
	asOf: "2026-10-19T02:00:00Z",
	...(mine ? { mine } : {}),
});

const version: Subject = { kind: "version", train: "21-2026", app: "di" };

describe("mineWhy", () => {
	it("names the release MR or the release ticket for an owner rule", () => {
		expect(mineWhy(hit("C1", version, [{ why: "release_mr_author", ref: "!77" }]))).toBe("your release MR !77");
		expect(mineWhy(hit("D2", version, [{ why: "release_ticket_assignee", ref: "DEMO-9" }]))).toBe(
			"your release ticket",
		);
	});

	it("says the release MR before the release ticket, whatever the wire order", () => {
		expect(
			mineWhy(
				hit("B1", version, [
					{ why: "release_ticket_assignee", ref: "DEMO-9" },
					{ why: "release_mr_author", ref: "!77" },
				]),
			),
		).toBe("your release MR !77 · your release ticket");
	});

	it("says only that I have not approved for D3", () => {
		expect(mineWhy(hit("D3", version, [{ why: "approver" }]))).toBe("you have not approved");
	});

	it("names only my cards, once each, for a page family", () => {
		const g5 = hit(
			"G5",
			{ kind: "pageFamily", train: "21-2026", family: "demo", bucket: "open", keys: ["A-1", "A-2", "A-3", "A-4"] },
			[
				{ why: "card", ref: "A-2" },
				{ why: "card", ref: "A-4" },
				{ why: "card", ref: "A-2" },
			],
		);
		expect(mineWhy(g5)).toBe("your cards A-2, A-4");
	});

	it("names one card in the singular", () => {
		expect(mineWhy(hit("G2", { kind: "card", key: "A-1", board: "A" }, [{ why: "card", ref: "A-1" }]))).toBe(
			"your card A-1",
		);
	});

	it("names my MR on an MR alert and my hotfix MR on a hotfix alert", () => {
		const mr: Subject = { kind: "mr", app: "di", iid: 12, source: "feat/A-1", target: "x" };
		expect(mineWhy(hit("L9", mr, [{ why: "mr_author", ref: "!12" }]))).toBe("your MR !12");
		expect(
			mineWhy(
				hit("F2", { kind: "hotfixTrain", app: "da", version: "2.3.1", train: "21-2026" }, [
					{ why: "mr_author", ref: "!88" },
				]),
			),
		).toBe("your hotfix MR !88");
		expect(
			mineWhy(
				hit("E1", { kind: "hotfix", app: "da", version: "2.3.1" }, [
					{ why: "mr_author", ref: "!88" },
					{ why: "mr_author", ref: "!89" },
				]),
			),
		).toBe("your hotfix MRs !88, !89");
	});

	it("never counts a No MR needed mark", () => {
		expect(mineWhy(hit("G2", { kind: "card", key: "A-1", board: "A" }, [{ why: "no_mr" }]))).toBe("");
	});

	it("is empty when the hit is not mine", () => {
		expect(mineWhy(hit("A1", version))).toBe("");
	});
});

describe("minePolicyOf", () => {
	it("reads the rule's policy from the catalogue", () => {
		const catalogue = [{ id: "D3", minePolicy: "pending_approver" }] as RuleInfo[];
		expect(minePolicyOf(catalogue, "D3")).toBe("pending_approver");
		expect(minePolicyOf(catalogue, "Z9")).toBeNull();
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

describe("alertText", () => {
	const d3 = hit("D3", version, [{ why: "approver" }]);
	d3.text = "2 of 3 not approved: Ann, Bob";
	d3.evidence = { pending: ["Ann", "Bob"], total: 3 };

	it("drops the other approvers' names from D3 under Only me", () => {
		expect(alertText(d3, true)).toBe("2 of 3 not approved");
	});

	it("keeps the rule's text with Only me off and on every other rule", () => {
		expect(alertText(d3, false)).toBe("2 of 3 not approved: Ann, Bob");
		const c1 = { ...hit("C1", version), text: "!77 failed: build" };
		expect(alertText(c1, true)).toBe("!77 failed: build");
	});
});

describe("the subjects board under Only me", () => {
	it("lists exactly the alerts the mine policies allow, each with its why phrase", () => {
		const b = subjectsBoard("2026-10-19");
		const mine = b.alerts.hits.filter((h) => h.bucket === "badge" && h.mine?.length);
		expect(Object.fromEntries(mine.map((h) => [h.id, mineWhy(h)]))).toEqual({
			"C5~v/21-2026/di": "your release MR !77",
			"C1~v/21-2026/di": "your release MR !77",
			"D3~v/21-2026/da": "you have not approved",
			"E1~h/da/2.3.1": "your hotfix MR !88",
			"F2~ht/da/2.3.1/21-2026": "your hotfix MR !88",
			"G5~pf/21-2026/demo/open": "your cards DEMO-104, DEMO-106",
		});
	});
});
