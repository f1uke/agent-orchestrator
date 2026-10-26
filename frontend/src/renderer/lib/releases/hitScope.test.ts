import { describe, expect, it } from "vitest";
import { cardHits, llbHits, splitKeys, versionHits } from "./hitScope";
import { board } from "./testkit";
import type { AlertHit, BoardApp, LLBGroup } from "./types";

function hit(id: string, over: Partial<AlertHit> = {}): AlertHit {
	return {
		id,
		rule: id.slice(0, id.indexOf("~")),
		subject: { kind: "version", train: "21-2026", app: "di" },
		severity: "risk",
		bucket: "badge",
		text: "",
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

describe("card keys in alert text", () => {
	it("splits out each named key and leaves the rest as text", () => {
		expect(splitKeys('2 card(s) not on "Train #21": DEMO-7, DEMO-11', ["DEMO-11", "DEMO-7"])).toEqual([
			{ text: '2 card(s) not on "Train #21": ' },
			{ key: "DEMO-7" },
			{ text: ", " },
			{ key: "DEMO-11" },
		]);
	});

	it("does not cut a longer key that starts with a named one", () => {
		expect(splitKeys("DEMO-110, DEMO-11", ["DEMO-11"])).toEqual([{ text: "DEMO-110, " }, { key: "DEMO-11" }]);
	});

	it("keeps text without keys whole", () => {
		expect(splitKeys("Jira says Done", [])).toEqual([{ text: "Jira says Done" }]);
	});
});
