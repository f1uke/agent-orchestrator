import { describe, expect, it } from "vitest";
import { cardHits, cardLine, llbHits, versionHits } from "./hitScope";
import { demoBoard } from "./fixture";
import { board } from "./testkit";
import type { AlertHit, BoardApp, LLBGroup } from "./types";

function hit(id: string, over: Partial<AlertHit> = {}): AlertHit {
	return {
		id,
		rule: id.slice(0, id.indexOf("~")),
		subject: { kind: "version", train: "21-2026", app: "di" },
		severity: "risk",
		bucket: "badge",
		copy: { what: "", do: "" },
		cards: [],
		evidence: {},
		links: [],
		asOf: "2026-10-15T00:00:00Z",
		...over,
	};
}

const page = (family: string, keys: string[], train = "21-2026"): Partial<AlertHit> => ({
	train,
	subject: { kind: "pageFamily", train, family, bucket: "merged", keys },
});

const card = (key: string) => ({ kind: "card" as const, key, board: "DEMO" });

describe("hits about a card", () => {
	const hits = [
		hit("G1~c/DEMO-11", { subject: card("DEMO-11") }),
		hit("L3~ct/21-2026/di/DEMO-11", {
			subject: { kind: "cardOnTrain", train: "21-2026", app: "di", key: "DEMO-11" },
		}),
		hit("G1~c/DEMO-110", { subject: card("DEMO-110") }),
		hit("G5~p/21-2026/demo/merged", page("demo", ["DEMO-7", "DEMO-11"])),
		hit("G5~p/21-2026/other/merged", page("other", ["DEMO-110"])),
		hit("L8~mr/di/415", {
			subject: { kind: "mr", app: "di", iid: 415, source: "feat/DEMO-11", target: "develop" },
			evidence: { card: "DEMO-11", target: "develop" },
		}),
		hit("L9~mr/di/416", {
			subject: { kind: "mr", app: "di", iid: 416, source: "feat/DEMO-11-x", target: "feat/DEMO-110" },
			evidence: { mr: 416 },
		}),
		hit("C1~v/21-2026/di"),
	];

	it("finds the card as a subject, as a missing card on a train page, and as the card an MR carries", () => {
		expect(cardHits(hits, "DEMO-11").map((h) => h.id)).toEqual([
			"G1~c/DEMO-11",
			"L3~ct/21-2026/di/DEMO-11",
			"G5~p/21-2026/demo/merged",
			"L8~mr/di/415",
		]);
	});

	it("matches whole keys, never a key prefix or a branch name", () => {
		expect(cardHits(hits, "DEMO-1")).toEqual([]);
		expect(cardHits(hits, "DEMO-110").map((h) => h.id)).toEqual(["G1~c/DEMO-110", "G5~p/21-2026/other/merged"]);
	});
});

describe("hits about a release", () => {
	const apps: BoardApp[] = [
		{ id: "di", name: "Demo iOS", family: "demo", platform: "ios" },
		{ id: "da", name: "Demo Android", family: "demo", platform: "android" },
		{ id: "pi", name: "Partner iOS", family: "partner", platform: "ios" },
	];
	const b = board({ apps });
	const hits = [
		hit("C1~v/21-2026/di", { train: "21-2026", app: "di" }),
		hit("C1~v/21-2026/da", { train: "21-2026", app: "da", subject: { kind: "version", train: "21-2026", app: "da" } }),
		hit("C1~v/22-2026/di", { train: "22-2026", app: "di", subject: { kind: "version", train: "22-2026", app: "di" } }),
		hit("G5~p/21-2026/demo/merged", page("demo", ["DEMO-7"])),
		hit("G5~p/21-2026/partner/merged", page("partner", ["DEMO-8"])),
		hit("G5~p/22-2026/demo/merged", page("demo", ["DEMO-9"], "22-2026")),
		hit("F1~tp/20-2026/21-2026", { train: "21-2026", subject: { kind: "trainPair", a: "20-2026", b: "21-2026" } }),
		hit("G1~c/DEMO-7", { subject: card("DEMO-7") }),
	];

	it("takes the app's own hits, its family's train page and train-wide hits of that train only", () => {
		expect(versionHits(hits, b, "21-2026", "di").map((h) => h.id)).toEqual([
			"C1~v/21-2026/di",
			"G5~p/21-2026/demo/merged",
			"F1~tp/20-2026/21-2026",
		]);
		expect(versionHits(hits, b, "21-2026", "pi").map((h) => h.id)).toEqual([
			"G5~p/21-2026/partner/merged",
			"F1~tp/20-2026/21-2026",
		]);
	});
});

describe("hits about a long-lived branch group", () => {
	const wallet = (id: string, over: Partial<AlertHit> = {}) =>
		hit(id, { evidence: { group: "demo-wallet", branch: "feature/demo-wallet" }, ...over });
	const hits = [
		wallet("L10~llb/22-2026/di/feature/demo-wallet", {
			subject: { kind: "llbBranch", train: "22-2026", app: "di", branch: "feature/demo-wallet", group: "demo-wallet" },
		}),
		wallet("L11~ct/22-2026/di/DEMO-7", {
			subject: { kind: "cardOnTrain", train: "22-2026", app: "di", key: "DEMO-7" },
		}),
		wallet("L1~mr/di/330", {
			subject: { kind: "mr", app: "di", iid: 330, source: "feature/demo-wallet", target: "develop" },
		}),
		hit("L10~llb/22-2026/di/feature/demo-pay", { evidence: { group: "demo-pay", branch: "feature/demo-pay" } }),
		hit("C1~v/22-2026/di"),
	];

	it("takes every hit whose evidence names the group, whatever its subject", () => {
		const g = { id: "demo-wallet" } as LLBGroup;
		expect(llbHits(hits, g).map((h) => h.id)).toEqual([
			"L10~llb/22-2026/di/feature/demo-wallet",
			"L11~ct/22-2026/di/DEMO-7",
			"L1~mr/di/330",
		]);
	});
});

describe("the cards line of an alert", () => {
	const b = demoBoard("2026-10-14");
	const listed = (cards: string[], what: string, over: Partial<AlertHit> = {}) =>
		hit("G5~pf/21-2026/demo/open", { cards, copy: { what, do: "" }, ...over });

	it("lists the cards that line 1 does not name", () => {
		const h = listed(["DEMO-101", "DEMO-102"], "2 cards are late");
		expect(cardLine(h, b, false, h.copy.what)).toEqual({ keys: ["DEMO-101", "DEMO-102"], more: 0 });
		const named = listed(["DEMO-101", "DEMO-102"], "DEMO-101 is late");
		expect(cardLine(named, b, false, named.copy.what)).toEqual({ keys: ["DEMO-102"], more: 0 });
	});

	it("shows the card's Jira summary when line 1 names the only card", () => {
		const h = listed(["DEMO-101"], "DEMO-101 has no MR yet");
		expect(cardLine(h, b, false, h.copy.what)).toEqual({ summary: "Sign-in with a passkey" });
	});

	it("shows nothing when line 1 names the only card and the board has no summary for it", () => {
		const h = listed(["NOPE-9"], "NOPE-9 has no MR yet");
		expect(cardLine(h, b, false, h.copy.what)).toBeNull();
	});

	it("does not mistake a longer key for the one line 1 names", () => {
		const h = listed(["DEMO-10"], "DEMO-101 is late");
		expect(cardLine(h, b, false, h.copy.what)).toEqual({ keys: ["DEMO-10"], more: 0 });
	});

	it("shows five keys, then how many more", () => {
		const keys = ["A-1", "A-2", "A-3", "A-4", "A-5", "A-6", "A-7"];
		const h = listed(keys, "7 cards are late");
		expect(cardLine(h, b, false, h.copy.what)).toEqual({ keys: keys.slice(0, 5), more: 2 });
	});

	it("shows nothing for a hit with no cards", () => {
		const h = listed([], "Train 21 has no release MR yet");
		expect(cardLine(h, b, false, h.copy.what)).toBeNull();
	});

	it("narrows a page hit to the person's own cards under Only me", () => {
		const h = listed(["A-1", "A-2", "A-3"], "3 cards are not on the page", {
			subject: { kind: "pageFamily", train: "21-2026", family: "demo", bucket: "merged", keys: ["A-1", "A-2", "A-3"] },
			mine: [{ why: "card", ref: "A-3" }],
		});
		expect(cardLine(h, b, true, h.copy.what)).toEqual({ keys: ["A-3"], more: 0 });
		expect(cardLine(h, b, false, h.copy.what)).toEqual({ keys: ["A-1", "A-2", "A-3"], more: 0 });
	});
});
