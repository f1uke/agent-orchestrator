import { shortDay } from "./day";
import { shortBranch, trainLabel } from "./llb";
import type { Board, BoardApp, Dest, DestKind, MineReason, SprintCard, SprintRow } from "./types";
import type { Tone } from "./vocab";

export const DEST_META: Readonly<Record<DestKind, { tone: Tone; glyph: string; label: string }>> = {
	sprint: { tone: "done", glyph: "✓", label: "sprint branch" },
	sprint_open: { tone: "running", glyph: "◐", label: "MR open → sprint branch" },
	llb: { tone: "hotfix", glyph: "⑂", label: "long-lived" },
	llb_open: { tone: "hotfix", glyph: "◐", label: "MR open → long-lived" },
	prev: { tone: "waiting", glyph: "↩", label: "previous sprint branch" },
	back: { tone: "waiting", glyph: "↩", label: "sprint branch → develop" },
	before: { tone: "todo", glyph: "✓", label: "merged before this sprint" },
	develop: { tone: "failed", glyph: "!", label: "straight into develop" },
	develop_open: { tone: "failed", glyph: "!", label: "MR open → develop" },
	release: { tone: "accent", glyph: "⇡", label: "release / main" },
	other: { tone: "waiting", glyph: "?", label: "other branch" },
	none: { tone: "todo", glyph: "·", label: "no MR yet" },
};

/** Lower is the card's leading kind: what goes wrong first, then what is still moving. */
const LEAD_ORDER: readonly DestKind[] = [
	"develop",
	"develop_open",
	"other",
	"llb_open",
	"llb",
	"sprint_open",
	"sprint",
	"back",
	"prev",
	"release",
	"before",
	"none",
];

export function leadKind(c: SprintCard): DestKind {
	const kinds = c.routes.map((r) => r.kind);
	return LEAD_ORDER.find((k) => kinds.includes(k)) ?? "none";
}

export function destKey(d: Dest): string {
	switch (d.kind) {
		case "train":
			return `t:${d.train}`;
		case "llb":
			return `l:${d.group}`;
		case "before":
			return "early";
		case "off":
			return "off";
		case "none":
			return "none";
	}
}

export function destTone(b: Board, d: Dest): Tone {
	switch (d.kind) {
		case "train": {
			const t = b.trains.find((x) => x.id === d.train);
			return t?.phase === "past" ? "done" : t?.phase === "current" ? "accent" : "todo";
		}
		case "llb":
			return "hotfix";
		case "before":
			return "done";
		case "off":
			return "failed";
		case "none":
			return "todo";
	}
}

export function destLabel(b: Board, d: Dest): string {
	switch (d.kind) {
		case "train":
			return `→ ${trainLabel(b, d.train)}`;
		case "llb":
			return `⑂ ${shortBranch(d.branch)}`;
		case "before":
			return `→ before ${trainLabel(b, d.train)}`;
		case "off":
			return "! off the sprint flow";
		case "none":
			return "· not routed yet";
	}
}

function destChip(b: Board, d: Dest, n: number): string {
	switch (d.kind) {
		case "train":
			return `${trainLabel(b, d.train)} ${n}`;
		case "llb":
			return `⑂ ${n}`;
		case "before":
			return `<${trainLabel(b, d.train)} ${n}`;
		case "off":
			return `off ${n}`;
		case "none":
			return `no MR ${n}`;
	}
}

export type BoardLabel = { title: string; dates: string; scope: string; dests: { text: string; tone: Tone }[] };

export function boardLabel(row: SprintRow, b: Board): BoardLabel {
	const c = row.counts;
	const scope = [
		`${c.new} new`,
		c.carriedWithMrs ? `${c.carriedWithMrs} carried with MRs` : "",
		c.carriedDone ? `${c.carriedDone} carried, done` : "",
	].filter(Boolean);
	return {
		title: `${row.board.key} ${row.number}`,
		dates: `${row.sprint.start ? shortDay(row.sprint.start) : "?"} → ${row.sprint.end ? shortDay(row.sprint.end) : "?"}`,
		scope: scope.join(" · "),
		dests: row.dests.map((d) => ({ text: destChip(b, d.to, d.count), tone: destTone(b, d.to) })),
	};
}

export type PillApp = { app: string; platform: BoardApp["platform"]; kind: DestKind; tone: Tone; mrs: number[] };
export type MrPill = { family: string; label: string; apps: PillApp[] };

/** One pill per app family, so pills line up down a list; an app without an MR draws nothing. */
export function mrPills(c: SprintCard, apps: readonly BoardApp[]): MrPill[] {
	const families = [...new Set(apps.map((a) => a.family))];
	return families.map((family) => ({
		family,
		label: family.slice(0, 3).replace(/^./, (x) => x.toUpperCase()),
		apps: apps
			.filter((a) => a.family === family)
			.flatMap((a) => {
				const mrs = c.mrs.filter((m) => m.app === a.id);
				if (!mrs.length) return [];
				const lead = mrs.find((m) => m.state === "merged") ?? mrs[0];
				return [
					{
						app: a.id,
						platform: a.platform,
						kind: lead.dest,
						tone: DEST_META[lead.dest].tone,
						mrs: mrs.map((m) => m.iid),
					},
				];
			}),
	}));
}

const MINE_TEXT: Readonly<Record<MineReason["why"], string>> = {
	assignee: "assignee",
	subtask: "subtask",
	mr_author: "your MR",
	release_mr_author: "your release MR",
	release_ticket_assignee: "release ticket",
	approver: "approver",
	card: "your card",
	landing_author: "your landing MR",
};

export function mineText(mine: readonly MineReason[]): string {
	return mine.map((m) => (m.ref ? `${MINE_TEXT[m.why]} ${m.ref}` : MINE_TEXT[m.why])).join(" · ");
}
