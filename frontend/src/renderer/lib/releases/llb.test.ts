import { describe, expect, it } from "vitest";
import { DW, timeline } from "./layout";
import { branchState, llbGroupsFor, llbLanes, llbWire, LLB_LANE_TOP } from "./llb";
import { board, train, version } from "./testkit";
import type { LLBGroup, LongLivedBranch } from "./types";

const branch = (name: string, over: Partial<LongLivedBranch> = {}): LongLivedBranch => ({
	app: "di",
	branch: name,
	openedOn: "2026-09-21",
	lastIn: "2026-10-01",
	state: "open",
	cards: [],
	...over,
});

const t21 = train("21-2026", "2026-10-13", "2026-10-20", "current", [
	version("21-2026", "di", "2.4.0", "2026-10-13", "2026-10-20"),
]);
const t22 = train("22-2026", "2026-10-27", "2026-11-03", "next", [
	version("22-2026", "di", "2.5.0", "2026-10-27", "2026-11-03"),
]);

const planned: LLBGroup = {
	id: "demo-wallet",
	name: "demo-wallet",
	branches: [branch("feature/demo-wallet")],
	target: { kind: "planned", train: "22-2026" },
};
const landed: LLBGroup = {
	id: "demo-chat",
	name: "demo-chat",
	branches: [
		branch("feature/demo-chat", {
			state: "landed_wrong",
			train: "21-2026",
			landing: {
				iid: 31,
				project: 1,
				title: "chat",
				state: "merged",
				author: "Kai",
				sourceBranch: "feature/demo-chat",
				targetBranch: "develop",
				createdOn: "2026-10-08",
				mergedOn: "2026-10-09",
				url: "",
			},
		}),
	],
	target: { kind: "landed", train: "21-2026" },
	plan: {
		id: "demo-chat",
		name: "Chat",
		targetKind: "train",
		targetTrain: "21-2026",
		note: "",
		branches: [],
		accepted: [{ mrRef: "di!31", reason: "shipped as intended" }],
		version: 2,
		updatedAt: "2026-10-10T00:00:00Z",
		updatedBy: "app",
	},
};
const none: LLBGroup = {
	id: "demo-gone",
	name: "demo-gone",
	branches: [branch("feature/demo-gone", { gone: true, lastIn: "2026-09-25" })],
	target: { kind: "none" },
};

const b = board({ today: "2026-10-14", trains: [t21, t22], llbs: [planned, landed, none] });
const tl = timeline(b);
const centre = (d: string) => tl.x(d) + DW / 2;

describe("long-lived lanes", () => {
	const lanes = llbLanes(b, "di", tl);
	const byName = Object.fromEntries(lanes.map((l) => [l.branch.branch, l]));

	it("stacks one lane per branch, 24 px apart under the summary line, and leaves out a deleted group", () => {
		expect(lanes.map((l) => [l.branch.branch, l.top])).toEqual([
			["feature/demo-wallet", LLB_LANE_TOP],
			["feature/demo-chat", LLB_LANE_TOP + 24],
		]);
	});

	it("runs an open branch from its first MR to today and reaches on to a planned train's freeze", () => {
		const l = byName["feature/demo-wallet"];
		expect([l.x0, l.x1, l.xr]).toEqual([centre("2026-09-21"), centre("2026-10-14"), centre("2026-10-27")]);
		expect(l.tone).toBe("hotfix");
		expect(l.target).toEqual({ text: "→ #22", tone: "accent" });
		expect(l.train).toBe("22-2026");
	});

	it("ends a landed branch at its merge and an accepted wrong landing reads done", () => {
		const l = byName["feature/demo-chat"];
		expect(l.x1).toBe(centre("2026-10-09"));
		expect(l.tone).toBe("done");
		expect(l.accepted).toBe(true);
		expect(l.target).toEqual({ text: "in #21", tone: "done" });
	});

	it("names each branch state, an accepted wrong landing as accepted", () => {
		expect(branchState(none, none.branches[0])).toEqual({ label: "deleted", tone: "todo" });
		expect(branchState(landed, landed.branches[0])).toEqual({ label: "accepted", tone: "done" });
		expect(branchState(planned, planned.branches[0])).toEqual({ label: "parked", tone: "hotfix" });
	});
});

describe("elbow wire", () => {
	const bar = { left: tl.x("2026-10-27") + DW / 2 - 9, right: tl.x("2026-11-03") + DW / 2 + 9, top: 200 };

	it("draws a planned branch as one path from its lane end along to the freeze day, then down into the bar", () => {
		const lane = llbLanes(b, "di", tl).find((l) => l.branch.branch === "feature/demo-wallet")!;
		const w = llbWire(lane, bar);
		const y = lane.top + 10;
		expect(w.d).toBe(`M${lane.x1} ${y} H${centre("2026-10-27")} V200`);
		expect(w.dot).toEqual({ x: centre("2026-10-27"), y: 200 });
		expect(w.planned).toBe(true);
	});

	it("turns a merged branch down with a 6 px radius when its merge day lies left of the bar", () => {
		const lane = llbLanes(b, "di", tl).find((l) => l.branch.branch === "feature/demo-chat")!;
		const w = llbWire(lane, bar);
		const y = lane.top + 10;
		const xc = bar.left + 9;
		expect(w.d).toBe(`M${lane.x1} ${y} H${xc - 6} Q${xc} ${y} ${xc} ${y + 6} V200`);
		expect(w.dot.x).toBe(xc);
		expect(w.planned).toBe(false);
	});

	it("drops straight down when the merge day sits over the bar", () => {
		const lane = { ...llbLanes(b, "di", tl).find((l) => l.branch.branch === "feature/demo-chat")!, x1: bar.left + 40 };
		expect(llbWire(lane, bar).d).toBe(`M${bar.left + 40} ${lane.top + 10} V200`);
	});
});

describe("llbGroupsFor", () => {
	const branch = (app: string, state: LLBGroup["branches"][number]["state"], mergedOn?: string) => ({
		app,
		branch: `feature/${app}-${state}`,
		openedOn: "2026-08-01",
		lastIn: "2026-10-01",
		state,
		cards: [],
		landing: mergedOn
			? {
					iid: 1,
					project: 1,
					title: "",
					state: "merged",
					author: "",
					sourceBranch: "",
					targetBranch: "sprint",
					createdOn: mergedOn,
					mergedOn,
					url: "",
				}
			: undefined,
	});
	const group = (id: string, target: LLBGroup["target"], ...branches: ReturnType<typeof branch>[]): LLBGroup => ({
		id,
		name: id,
		target,
		branches,
	});
	const b = board({
		today: "2026-10-15",
		llbs: [
			group("open", { kind: "none" }, branch("di", "open")),
			group(
				"just-landed",
				{ kind: "landed", train: "21-2026" },
				branch("di", "landed_wrong", "2026-10-12"),
				branch("da", "landed", "2026-10-12"),
			),
			group("old", { kind: "landed", train: "18-2026" }, branch("di", "landed", "2026-09-14")),
			group("dropped", { kind: "dropped" }, branch("di", "open")),
			group("other-app", { kind: "none" }, branch("pa", "open")),
		],
	});

	it("keeps groups still open or landed within the last 21 days, for the app's own branches", () => {
		expect(llbGroupsFor(b, "di").map((g) => g.id)).toEqual(["open", "just-landed"]);
		expect(llbGroupsFor(b, "da").map((g) => g.id)).toEqual(["just-landed"]);
		expect(llbGroupsFor(b, "pi")).toEqual([]);
	});
});
