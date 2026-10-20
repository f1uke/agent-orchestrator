import { describe, expect, it } from "vitest";
import { demoBoard, subjectsBoard } from "./fixture";
import { LOCATORS, locate, type RevealView, type Target } from "./locate";
import { DEFAULT_LAYERS } from "./url";
import type { SubjectKind } from "./types";

const TODAY = "2026-10-14";
const view = (over: Partial<RevealView> = {}): RevealView => ({
	layers: DEFAULT_LAYERS,
	hiddenBoards: new Set(),
	apps: null,
	focus: "21-2026",
	me: false,
	mobile: false,
	...over,
});

const EXPECTED: Record<string, { target: Target; fallback: boolean }> = {
	"C5~v/21-2026/di": { target: "version-bar", fallback: false },
	"A6~vd/21-2026/di/freeze": { target: "stage-marker", fallback: false },
	"A2~jv/7001": { target: "app-label", fallback: true },
	"A3~m/pi/9001": { target: "app-label", fallback: true },
	"E1~h/da/2.3.1": { target: "hotfix-bar", fallback: false },
	"F2~ht/da/2.3.1/21-2026": { target: "hotfix-bar", fallback: false },
	"F1~tt/21-2026/22-2026": { target: "train-chip", fallback: true },
	"L3~ct/21-2026/di/DEMO-109": { target: "card-row", fallback: false },
	"G2~c/DEMO-304": { target: "sprint-card", fallback: false },
	"L1~mr/di/330": { target: "llb-lane", fallback: false },
	"L8~mr/pi/405": { target: "mr-glyph", fallback: false },
	"L9~mr/da/415": { target: "app-label", fallback: true },
	"G5~pf/21-2026/demo/open": { target: "card-row", fallback: false },
};

describe("locate", () => {
	const b = subjectsBoard(TODAY);
	const hit = (id: string) => {
		const h = b.alerts.hits.find((x) => x.id === id);
		if (!h) throw new Error(`no hit ${id} on the subjects board`);
		return h;
	};

	it.each(Object.entries(EXPECTED))("finds %s on the timeline", (id, want) => {
		const r = locate(hit(id).subject, b, view());
		expect({ target: r.target, fallback: r.fallback !== null }).toEqual(want);
		expect(r.selectors.length).toBeGreaterThan(0);
	});

	it("has an alert on the subjects board for every subject kind", () => {
		const kinds = new Set(b.alerts.hits.map((h) => h.subject.kind));
		expect([...kinds].sort()).toEqual((Object.keys(LOCATORS) as SubjectKind[]).sort());
	});

	it("maps every rule in the catalogue to a locator for each subject kind it declares", () => {
		for (const r of b.catalogue)
			for (const k of r.subjects) expect(LOCATORS[k], `${r.id} declares ${k}`).toBeTypeOf("function");
	});

	it("turns the sprint layer on and opens the row's folds for a card", () => {
		const r = locate(hit("G2~c/DEMO-304").subject, b, view());
		expect(r.layers).toEqual(["sprint"]);
		expect(r.folds).toEqual(["sp-DEMO", "sp-DEMO:none"]);
		expect(r.unhide).toEqual([]);
		expect(r.selectors).toEqual([
			'[data-testid="sprint-row-DEMO"] [data-sprint="active"][data-dest="none"] .crow[data-key="DEMO-304"]',
		]);
	});

	it("unhides the board of a card on a hidden row", () => {
		const r = locate(hit("G2~c/PIL-12").subject, b, view({ hiddenBoards: new Set(["PILOT"]) }));
		expect(r.unhide).toEqual(["PILOT"]);
		expect(r.folds).toEqual(["sp-PILOT", "sp-PILOT:t:21-2026"]);
	});

	it("turns Mobile only off for a card that is not mobile, and aims at its own block", () => {
		const web = structuredClone(b);
		const c = web.sprints.flatMap((x) => x.cards).find((x) => x.key === "PIL-12");
		if (!c) throw new Error("fixture lost PIL-12");
		Object.assign(c, { mobile: false, active: false, activeWhy: [], routes: [], mrs: [] });
		const on = locate(hit("G2~c/PIL-12").subject, web, view({ mobile: true }));
		expect(on.notMobile).toBe(true);
		expect(on.selectors[0]).toContain('[data-dest="web"]');
		expect(locate(hit("G2~c/PIL-12").subject, web, view({ mobile: false })).notMobile).toBe(false);
		expect(locate(hit("G2~c/DEMO-304").subject, b, view({ mobile: true })).notMobile).toBe(false);
	});

	it("shows the card's app when the app filter hides it", () => {
		const r = locate(hit("G2~c/PIL-12").subject, b, view({ apps: new Set(["di"]) }));
		expect(r.apps).toEqual(["pa"]);
	});

	it("opens the card group and the long-lived branch a parked card sits in", () => {
		const r = locate(hit("L3~ct/21-2026/di/DEMO-109").subject, b, view());
		expect(r.layers).toEqual(["cards"]);
		expect(r.folds).toEqual(["di:21-2026", "di:21-2026:llb:feature/demo-search"]);
	});

	it("focuses the train whose stage marker it needs, unless History draws every marker", () => {
		const s = hit("A6~vd/21-2026/di/freeze").subject;
		expect(locate(s, b, view({ focus: "22-2026" })).focus).toBe("21-2026");
		expect(locate(s, b, view({ focus: "21-2026" })).focus).toBeNull();
		expect(locate(s, b, view({ focus: "22-2026", layers: { ...DEFAULT_LAYERS, past: true } })).focus).toBeNull();
	});

	it("turns Only me off for a target Only me does not draw, and leaves it on for one of mine", () => {
		const me = view({ me: true });
		expect(locate(hit("G2~c/DEMO-304").subject, b, me).notMine).toBe(true);
		expect(locate({ kind: "card", key: "DEMO-301", board: "DEMO" }, b, me).notMine).toBe(false);
		expect(locate(hit("L1~mr/di/330").subject, b, me).notMine).toBe(false);
		expect(locate(hit("E1~h/da/2.3.1").subject, b, me).notMine).toBe(false);
		expect(locate(hit("G2~c/DEMO-304").subject, b, view()).notMine).toBe(false);
	});

	it("finds a missing page card on any app of the family, past keys no list carries", () => {
		const r = locate(
			{ kind: "pageFamily", train: "21-2026", family: "demo", bucket: "open", keys: ["DEMO-999", "DEMO-109"] },
			b,
			view(),
		);
		expect(r.target).toBe("card-row");
		expect(r.selectors).toEqual(['[data-testid="cards-di:21-2026"] .crow[data-key="DEMO-109"]']);
		const none = locate(
			{ kind: "pageFamily", train: "21-2026", family: "demo", bucket: "open", keys: ["DEMO-999"] },
			b,
			view(),
		);
		expect(none.target).toBe("version-bar");
		expect(none.fallback).toMatch(/^at its train's bar/);
	});

	it("rings every missing card of a page family, on each app of the family whose list carries it", () => {
		const r = locate(hit("G5~pf/21-2026/demo/open").subject, b, view());
		expect(r.target).toBe("card-row");
		expect(r.selectors).toEqual([
			...["DEMO-103", "DEMO-104", "DEMO-105", "DEMO-106"].map(
				(k) => `[data-testid="cards-di:21-2026"] .crow[data-key="${k}"]`,
			),
			...["DEMO-103", "DEMO-104"].map((k) => `[data-testid="cards-da:21-2026"] .crow[data-key="${k}"]`),
		]);
		expect(r.layers).toEqual(["cards"]);
		expect(r.folds).toEqual(["di:21-2026", "da:21-2026"]);
		expect(r.apps).toEqual(["di", "da"]);
	});

	it("says where Show lands when the subject has no mark of its own", () => {
		expect(locate(hit("L9~mr/da/415").subject, b, view()).fallback).toBe(
			"at its app: the timeline draws no lane for feat/DEMO-201-base",
		);
		expect(locate(hit("F1~tt/21-2026/22-2026").subject, b, view()).fallback).toMatch(/^at the later train/);
		expect(locate(hit("C5~v/21-2026/di").subject, b, view()).fallback).toBeNull();
	});

	it("opens the app's long-lived lanes for an MR out of a long-lived branch", () => {
		const r = locate(hit("L1~mr/di/330").subject, b, view());
		expect(r.layers).toEqual(["llb"]);
		expect(r.folds).toEqual(["di:llbs"]);
	});

	it("turns hotfixes on for a hotfix", () => {
		expect(locate(hit("E1~h/da/2.3.1").subject, b, view()).hotfixes).toBe(true);
	});

	it("falls back to the app label when a train is older than the timeline", () => {
		const old = demoBoard(TODAY);
		const t = old.trains[0];
		t.publish = "2025-01-01";
		for (const v of t.versions) v.milestone.due = "2025-01-01";
		const r = locate({ kind: "version", train: t.id, app: "di" }, old, view());
		expect(r.target).toBe("app-label");
		expect(r.fallback).toMatch(/older than the timeline/);
	});

	it("falls back to the bar for a card on a published train", () => {
		const r = locate({ kind: "cardOnTrain", train: "20-2026", app: "di", key: "DEMO-1" }, b, view());
		expect(r.target).toBe("version-bar");
		expect(r.fallback).not.toBeNull();
	});
});
