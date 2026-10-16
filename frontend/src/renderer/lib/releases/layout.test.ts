import { describe, expect, it } from "vitest";
import {
	appLanes,
	barBox,
	cardGroupTop,
	DW,
	FOCUS_LANE,
	hotfixStages,
	LABEL_H,
	llbBlockH,
	markerSpots,
	QUIET_LANE,
	rowHeight,
	timeline,
} from "./layout";
import { board, hotfix, stage, train, version } from "./testkit";

const t20 = train("20-2026", "2026-09-29", "2026-10-06", "past", [
	version("20-2026", "di", "2.3.0", "2026-09-29", "2026-10-06", {
		stages: [stage("freeze", "2026-09-29", "done"), stage("publish", "2026-10-06", "done")],
	}),
]);
const t21 = train("21-2026", "2026-10-13", "2026-10-20", "current", [
	version("21-2026", "di", "2.4.0", "2026-10-13", "2026-10-20", {
		stages: [
			stage("freeze", "2026-10-13", "done"),
			stage("approve", "2026-10-15", "waiting"),
			stage("force", "2026-10-24", "todo"),
		],
	}),
]);
const t22 = train("22-2026", "2026-10-27", "2026-11-03", "next", [
	version("22-2026", "di", "2.5.0", "2026-10-27", "2026-11-03"),
]);

describe("timeline", () => {
	it("starts on the Monday eight weeks or more before today and ends 12 days after the last publish", () => {
		const tl = timeline(board({ today: "2026-10-14", trains: [t20, t21, t22] }));
		expect(tl.start).toBe("2026-08-17");
		expect(tl.end).toBe("2026-11-15");
		expect(tl.x("2026-08-17")).toBe(0);
		expect(tl.x("2026-08-18")).toBe(DW);
		expect(tl.width).toBe(tl.days * DW);
	});

	it("needs no sprint rows, which a cold start lacks for minutes", () => {
		const tl = timeline(board({ today: "2026-10-14", trains: [t21], sprints: [] }));
		expect(tl.start <= "2026-10-13").toBe(true);
		expect(tl.end).toBe("2026-11-01");
	});

	it("reaches back to an active sprint that began before the eight weeks", () => {
		const sprint = {
			board: { id: 1, key: "DEMO", ownsSprintBranch: true },
			number: 9,
			sprint: { board: 1, id: 9, name: "Demo 9", url: "", start: "2026-08-03", end: "2026-10-20" },
			cards: [],
			dests: [],
			counts: { new: 0, carriedDone: 0, carriedWithMrs: 0 },
			total: 0,
		};
		expect(timeline(board({ today: "2026-10-14", trains: [t21], sprints: [sprint] })).start).toBe("2026-08-03");
	});
});

describe("version bar geometry", () => {
	it("spans freeze-day centre to publish-day centre, 9 px past each so markers share its edges", () => {
		const tl = timeline(board({ today: "2026-10-14", trains: [t21] }));
		const box = barBox(tl, "2026-10-13", "2026-10-20");
		expect(box.left).toBe(tl.x("2026-10-13") + DW / 2 - 9);
		expect(box.width).toBe(7 * DW + 18);
	});
});

describe("app lanes", () => {
	const b = board({ today: "2026-10-14", trains: [t20, t21, t22] });
	const opts = { focus: "21-2026", past: false, hotfixes: true };

	it("gives markers to the focused train only, 56 px for its lane and 40 px for the rest", () => {
		const L = appLanes(b, timeline(b), "di", opts);
		expect(L.items.map((it) => [it.key, it.markers])).toEqual([
			["20-2026", false],
			["21-2026", true],
			["22-2026", false],
		]);
		const focused = L.items.find((it) => it.key === "21-2026");
		expect(L.laneH[focused?.lane ?? -1]).toBe(FOCUS_LANE);
		expect(L.laneH.filter((h) => h === QUIET_LANE).length).toBe(L.laneH.length - 1);
		expect(L.laneTop[0]).toBe(8);
		expect(L.lanesH).toBe(L.laneH.reduce((a, h) => a + h, 0));
	});

	it("gives every train markers with the History layer on", () => {
		const L = appLanes(b, timeline(b), "di", { ...opts, past: true });
		expect(L.items.every((it) => it.markers)).toBe(true);
		expect(L.laneH.every((h) => h === FOCUS_LANE)).toBe(true);
	});

	it("packs a train without markers by its freeze..publish window, ignoring a late stage", () => {
		const late = train("21-2026", "2026-10-13", "2026-10-20", "current", [
			version("21-2026", "di", "2.4.0", "2026-10-13", "2026-10-20", { stages: [stage("force", "2026-10-30", "todo")] }),
		]);
		const next = train("22-2026", "2026-10-22", "2026-10-29", "next", [
			version("22-2026", "di", "2.5.0", "2026-10-22", "2026-10-29"),
		]);
		const bb = board({ today: "2026-10-14", trains: [late, next] });
		const quiet = appLanes(bb, timeline(bb), "di", { ...opts, focus: "22-2026" });
		expect(quiet.items.map((it) => it.lane)).toEqual([0, 0]);
		const marked = appLanes(bb, timeline(bb), "di", { ...opts, focus: "21-2026" });
		expect(marked.items.map((it) => it.lane)).toEqual([0, 1]);
	});

	it("uses the app's own milestone dates, not the train's", () => {
		const split = train(
			"21-2026",
			"2026-10-13",
			"2026-10-20",
			"current",
			[version("21-2026", "di", "2.4.0", "2026-10-15", "2026-10-22")],
			{
				datesSplit: ["di"],
			},
		);
		const bb = board({ today: "2026-10-14", trains: [split] });
		const it = appLanes(bb, timeline(bb), "di", opts).items[0];
		expect([it.freeze, it.publish, it.offDates]).toEqual(["2026-10-15", "2026-10-22", true]);
	});

	it("keeps a hotfix still due marked and drops hotfixes with the toggle off", () => {
		const bb = board({
			today: "2026-10-14",
			trains: [t21],
			hotfixes: [hotfix("di", "2.3.1", "2026-10-14", "2026-10-16"), hotfix("di", "2.2.1", "2026-09-01", "2026-09-02")],
		});
		const L = appLanes(bb, timeline(bb), "di", opts);
		expect(L.items.filter((it) => it.kind === "hotfix").map((it) => [it.key, it.markers])).toEqual([
			["2.2.1", false],
			["2.3.1", true],
		]);
		expect(
			appLanes(bb, timeline(bb), "di", { ...opts, hotfixes: false }).items.every((it) => it.kind === "train"),
		).toBe(true);
	});

	it("leaves out a version that ended before the timeline starts", () => {
		const old = train("9-2026", "2026-04-28", "2026-05-05", "past", [
			version("9-2026", "di", "1.0.0", "2026-04-28", "2026-05-05"),
		]);
		const bb = board({ today: "2026-10-14", trains: [old, t21] });
		expect(appLanes(bb, timeline(bb), "di", opts).items.map((it) => it.key)).toEqual(["21-2026"]);
	});
});

describe("row height and markers", () => {
	it("never drops below the 134 px label, and grows with the long-lived block, the lanes and the card groups", () => {
		expect(rowHeight({ llbH: 0, lanesH: 40, cardsBottom: 0 })).toBe(LABEL_H);
		expect(rowHeight({ llbH: 30, lanesH: 3 * FOCUS_LANE, cardsBottom: 0 })).toBe(30 + 8 + 3 * FOCUS_LANE + 10);
		expect(rowHeight({ llbH: 30, lanesH: 40, cardsBottom: 400 })).toBe(410);
	});

	it("makes the long-lived block one 30 px line, plus 24 px per lane when unfolded", () => {
		expect(llbBlockH(0, true)).toBe(0);
		expect(llbBlockH(3, false)).toBe(30);
		expect(llbBlockH(3, true)).toBe(30 + 3 * 24 + 2);
	});

	it("puts a card group right under its own bar, or below every lane when a later lane would cover it", () => {
		const bb = board({ today: "2026-10-14", trains: [t21, t22] });
		const tl = timeline(bb);
		const L = appLanes(bb, tl, "di", { focus: "21-2026", past: false, hotfixes: true });
		const it21 = L.items.find((it) => it.key === "21-2026")!;
		expect(cardGroupTop(L, it21, tl, 30)).toBe(30 + L.laneTop[it21.lane] + FOCUS_LANE - 2);
		const crowded = { ...L, items: [...L.items, { ...it21, key: "x", lane: it21.lane + 1 }] };
		expect(cardGroupTop(crowded, it21, tl, 30)).toBe(30 + 8 + L.lanesH + 2);
	});

	it("puts same-day markers side by side, 15 px apart, centred on the day", () => {
		const tl = timeline(board({ today: "2026-10-14", trains: [t21] }));
		const spots = markerSpots(tl, [
			stage("tag", "2026-10-19", "todo"),
			stage("publish", "2026-10-19", "todo"),
			stage("force", undefined, "not_observable"),
		]);
		const centre = tl.x("2026-10-19") + DW / 2 - 8.5;
		expect(spots.map((s) => [s.stage.key, s.left])).toEqual([
			["tag", centre - 7.5],
			["publish", centre + 7.5],
		]);
	});
});

describe("hotfixStages", () => {
	it("reads the tag and the milestone, so each hotfix stage can name its sources", () => {
		const h = hotfix("di", "2.3.1", "2026-10-15", "2026-10-16", {
			tag: { name: "2.3.1", project: 1, url: "", commitOn: "2026-10-15", newest: true, mergesAfter: [] },
		});
		expect(hotfixStages(h, "2026-10-14").map((s) => [s.key, s.reads])).toEqual([
			["tag", ["gitlab.tags", "gitlab.milestones"]],
			["publish", ["gitlab.tags", "gitlab.milestones"]],
		]);
	});
});
