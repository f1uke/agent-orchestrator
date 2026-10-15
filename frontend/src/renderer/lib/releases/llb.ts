import { addDays, dayDiff } from "./day";
import { DW, type Timeline } from "./layout";
import type { AppId, Board, Day, LLBGroup, LongLivedBranch, TrainKey } from "./types";
import { LLB_STATE_META, type Tone } from "./vocab";

export const LLB_LANE_TOP = 32;
export const LLB_LANE_H = 24;
const RECENT_DAYS = 21;

export function shortBranch(name: string): string {
	return name.replace(/^feature\//, "");
}

/** d4 keeps a group while any branch is open, or landed within three weeks so the train it landed in still names it. */
export function llbGroupsFor(b: Board, app: AppId): LLBGroup[] {
	const since = addDays(b.today, -RECENT_DAYS);
	return b.llbs.filter((g) => {
		if (g.target.kind === "dropped" || !g.branches.some((x) => x.app === app)) return false;
		return g.branches.some(
			(x) => (x.state !== "landed" && x.state !== "landed_wrong" && !x.gone) || (x.landing?.mergedOn ?? "") >= since,
		);
	});
}

export function isOpenLlb(g: LLBGroup): boolean {
	return g.branches.some((b) => !b.landing?.mergedOn && !b.gone);
}

export function targetTrain(g: LLBGroup): TrainKey | null {
	return g.target.kind === "planned" || g.target.kind === "landed" ? g.target.train : null;
}

export function trainLabel(b: Board, key: TrainKey): string {
	return b.trains.find((t) => t.id === key)?.label ?? `#${key.replace(/-\d{4}$/, "")}`;
}

export function urgency(g: LLBGroup, b: Board): Tone {
	if (g.target.kind === "landed") return "done";
	if (g.target.kind !== "planned") return "todo";
	const t = g.target.train;
	const freeze = b.trains.find((x) => x.id === t)?.freeze;
	if (!freeze) return "accent";
	const days = dayDiff(b.today, freeze);
	return days < 0 ? "failed" : days <= 3 ? "waiting" : "accent";
}

function acceptedLanding(g: LLBGroup, br: LongLivedBranch): boolean {
	const l = br.landing;
	return !!l && (g.plan?.accepted ?? []).some((a) => a.mrRef === `${br.app}!${l.iid}`);
}

export function branchState(g: LLBGroup, br: LongLivedBranch): { label: string; tone: Tone } {
	if (br.gone && !br.landing) return { label: LLB_STATE_META.gone.short, tone: LLB_STATE_META.gone.tone };
	if (br.state === "landed_wrong" && acceptedLanding(g, br)) return { label: "accepted", tone: "done" };
	const m = LLB_STATE_META[br.state];
	return { label: m.short, tone: m.tone };
}

export function targetChip(g: LLBGroup, b: Board): { text: string; tone: Tone | null } | null {
	switch (g.target.kind) {
		case "planned":
			return { text: `→ ${trainLabel(b, g.target.train)}`, tone: urgency(g, b) };
		case "landed":
			return { text: `in ${trainLabel(b, g.target.train)}`, tone: "done" };
		case "dropped":
			return { text: "dropped", tone: null };
		case "none":
			return { text: "no target", tone: null };
	}
}

export type LLBLane = {
	group: LLBGroup;
	branch: LongLivedBranch;
	top: number;
	x0: number;
	x1: number;
	xr: number;
	tone: Tone;
	accepted: boolean;
	open: boolean;
	state: { label: string; tone: Tone };
	target: { text: string; tone: Tone | null } | null;
	train: TrainKey | null;
	keys: number;
	ticks: { key: string; x: number; tone: Tone; title: string }[];
};

function endDay(br: LongLivedBranch, today: Day): Day {
	return br.landing?.mergedOn ?? (br.gone ? br.lastIn : today);
}

export function llbLanes(b: Board, app: AppId, tl: Timeline): LLBLane[] {
	const centre = (d: Day) => tl.x(d) + DW / 2;
	const lanes: LLBLane[] = [];
	for (const group of llbGroupsFor(b, app)) {
		for (const branch of group.branches.filter((x) => x.app === app)) {
			const x0 = centre(branch.openedOn);
			const x1 = Math.max(centre(endDay(branch, b.today)), x0 + 2 * DW);
			const merged = !!branch.landing?.mergedOn;
			const planned = !merged && group.target.kind === "planned" ? group.target.train : null;
			const freeze = planned ? b.trains.find((t) => t.id === planned)?.freeze : undefined;
			const state = branchState(group, branch);
			lanes.push({
				group,
				branch,
				top: LLB_LANE_TOP + lanes.length * LLB_LANE_H,
				x0,
				x1,
				xr: freeze ? Math.max(x1, centre(freeze)) : x1,
				tone: state.tone,
				accepted: acceptedLanding(group, branch),
				open: !merged && !branch.gone,
				state,
				target: targetChip(group, b),
				train: merged ? (branch.train ?? null) : planned,
				keys: new Set(branch.cards.map((c) => c.key)).size,
				ticks: branch.cards.map((c) => ({
					key: c.key,
					x: centre(c.mr.mergedOn ?? c.mr.createdOn) - x0,
					tone: c.statusCategory === "done" ? "done" : c.statusCategory === "new" ? "todo" : "running",
					title: `${c.key} ${c.summary ?? ""} · ${c.status ?? ""}`,
				})),
			});
		}
	}
	return lanes;
}

const TURN = 6;

export function llbWire(
	lane: LLBLane,
	bar: { left: number; right: number; top: number },
): { d: string; dot: { x: number; y: number }; planned: boolean } {
	const y = lane.top + 10;
	const planned = !lane.branch.landing?.mergedOn;
	if (planned) {
		const xe = lane.xr;
		return { d: `M${lane.x1} ${y} H${xe} V${bar.top}`, dot: { x: xe, y: bar.top }, planned };
	}
	const xc = Math.max(bar.left + 9, Math.min(bar.right - 9, lane.x1));
	if (Math.abs(xc - lane.x1) <= 1)
		return { d: `M${lane.x1} ${y} V${bar.top}`, dot: { x: lane.x1, y: bar.top }, planned };
	const dir = Math.sign(xc - lane.x1);
	return {
		d: `M${lane.x1} ${y} H${xc - dir * TURN} Q${xc} ${y} ${xc} ${y + TURN} V${bar.top}`,
		dot: { x: xc, y: bar.top },
		planned,
	};
}
