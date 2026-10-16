import { addDays, dayDiff, maxDay, minDay, weekday } from "./day";
import type { AppId, AppVersion, Board, Day, Hotfix, Stage, Train, TrainKey } from "./types";

export const DW = 36;
export const LABEL_W = 280;
export const LABEL_H = 134;
export const FOCUS_LANE = 56;
export const QUIET_LANE = 40;
export const BAR_H = 34;
export const LLB_LINE_H = 30;
const LLB_LANE_H = 24;
const LANES_PAD = 8;
const ROW_PAD = 10;
const LOOKBACK_DAYS = 56;
const TAIL_DAYS = 12;

export type Timeline = {
	start: Day;
	end: Day;
	today: Day;
	days: number;
	width: number;
	x: (d: Day) => number;
};

function mondayOf(d: Day): Day {
	return addDays(d, -((weekday(d) + 6) % 7));
}

export function timeline(b: Board): Timeline {
	const sprintStarts = b.sprints.flatMap((s) => (s.sprint.start ? [s.sprint.start] : []));
	const start = mondayOf(minDay(addDays(b.today, -LOOKBACK_DAYS), ...sprintStarts));
	const lastDay = maxDay(
		b.today,
		...b.trains.flatMap((t) => (t.publish ? [t.publish] : [])),
		...b.hotfixes.flatMap((h) => (h.milestone.due ? [h.milestone.due] : [])),
	);
	const end = addDays(lastDay, TAIL_DAYS);
	const days = dayDiff(start, end) + 1;
	return { start, end, today: b.today, days, width: days * DW, x: (d) => dayDiff(start, d) * DW };
}

export function barBox(tl: Timeline, freeze: Day, publish: Day): { left: number; width: number } {
	return { left: tl.x(freeze) + DW / 2 - 9, width: tl.x(publish) - tl.x(freeze) + 18 };
}

type ItemBase = {
	key: string;
	freeze: Day;
	publish: Day;
	stages: Stage[];
	markers: boolean;
	lo: Day;
	hi: Day;
	lane: number;
};
export type LaneItem =
	| (ItemBase & { kind: "train"; train: Train; version: AppVersion; offDates: boolean })
	| (ItemBase & { kind: "hotfix"; hotfix: Hotfix; offDates: false });

export type AppLanes = { items: LaneItem[]; laneH: number[]; laneTop: number[]; lanesH: number };
export type LaneOptions = { focus: TrainKey | null; past: boolean; hotfixes: boolean };

function stageDates(stages: Stage[]): Day[] {
	return stages.flatMap((s) => (s.date ? [s.date] : []));
}

const HOTFIX_READS = ["gitlab.tags", "gitlab.milestones"];

export function hotfixStages(h: Hotfix, today: Day): Stage[] {
	const due = h.milestone.due;
	const out: Stage[] = [];
	if (h.tag)
		out.push({ key: "tag", date: h.tag.commitOn, status: "done", detail: `Tag ${h.tag.name}`, reads: HOTFIX_READS });
	if (due)
		out.push({
			key: "publish",
			date: due,
			status: h.tag && due <= today ? "done" : "todo",
			detail: "Hotfix due",
			reads: HOTFIX_READS,
		});
	return out;
}

function span(freeze: Day, publish: Day, stages: Stage[], markers: boolean): { lo: Day; hi: Day } {
	const ds = markers ? stageDates(stages) : [];
	return { lo: minDay(freeze, ...ds), hi: maxDay(publish, ...ds) };
}

export function appLanes(b: Board, tl: Timeline, app: AppId, o: LaneOptions): AppLanes {
	const items: LaneItem[] = [];
	for (const t of b.trains) {
		const v = t.versions.find((x) => x.app === app);
		const freeze = v?.milestone.start ?? t.freeze;
		const publish = v?.milestone.due ?? t.publish;
		if (!v || !freeze || !publish || publish < tl.start) continue;
		const markers = t.id === o.focus || o.past;
		const s = span(freeze, publish, v.stages, markers);
		items.push({
			kind: "train",
			key: t.id,
			train: t,
			version: v,
			freeze,
			publish,
			stages: v.stages,
			markers,
			...s,
			lane: 0,
			offDates: t.datesSplit.includes(app),
		});
	}
	if (o.hotfixes) {
		for (const h of b.hotfixes) {
			const freeze = h.milestone.start ?? h.milestone.due;
			const publish = h.milestone.due ?? h.milestone.start;
			if (h.app !== app || !freeze || !publish || publish < tl.start) continue;
			const stages = hotfixStages(h, b.today);
			const markers = o.past || publish >= b.today;
			items.push({
				kind: "hotfix",
				key: h.version,
				hotfix: h,
				freeze,
				publish,
				stages,
				markers,
				...span(freeze, publish, stages, markers),
				lane: 0,
				offDates: false,
			});
		}
	}
	items.sort((a, c) => (a.freeze < c.freeze ? -1 : a.freeze > c.freeze ? 1 : 0));
	const ends: Day[] = [];
	for (const it of items) {
		let i = ends.findIndex((end) => end < it.lo);
		if (i < 0) i = ends.push(it.hi) - 1;
		ends[i] = it.hi;
		it.lane = i;
	}
	const laneH = Array.from({ length: Math.max(1, ends.length) }, (_, i) =>
		items.some((it) => it.lane === i && it.markers) ? FOCUS_LANE : QUIET_LANE,
	);
	const laneTop = laneH.map((_, i) => LANES_PAD + laneH.slice(0, i).reduce((a, h) => a + h, 0));
	return { items, laneH, laneTop, lanesH: laneH.reduce((a, h) => a + h, 0) };
}

export function llbBlockH(lanes: number, open: boolean): number {
	if (!lanes) return 0;
	return LLB_LINE_H + (open ? lanes * LLB_LANE_H + 2 : 0);
}

const GROUP_REACH = 520;

export function cardGroupTop(L: AppLanes, it: LaneItem, tl: Timeline, llbH: number): number {
	const gx = tl.x(it.freeze) + 2;
	const covered = L.items.some(
		(o) => o !== it && o.lane > it.lane && tl.x(o.freeze) < gx + GROUP_REACH && tl.x(o.publish) + DW > gx,
	);
	return covered ? llbH + LANES_PAD + L.lanesH + 2 : llbH + L.laneTop[it.lane] + L.laneH[it.lane] - 2;
}

export function rowHeight({
	llbH,
	lanesH,
	cardsBottom,
}: {
	llbH: number;
	lanesH: number;
	cardsBottom: number;
}): number {
	return Math.max(llbH + LANES_PAD + lanesH + ROW_PAD, cardsBottom + ROW_PAD, LABEL_H);
}

export function markerSpots(tl: Timeline, stages: Stage[]): { stage: Stage; left: number }[] {
	const byDay = new Map<Day, Stage[]>();
	for (const s of stages) if (s.date) byDay.set(s.date, [...(byDay.get(s.date) ?? []), s]);
	return [...byDay.entries()].flatMap(([d, group]) =>
		group.map((stage, i) => ({ stage, left: tl.x(d) + DW / 2 + (i - (group.length - 1) / 2) * 15 - 8.5 })),
	);
}
