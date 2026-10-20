import { appMark } from "./appMark";
import { statusTone } from "./cards";
import { shortDay } from "./day";
import { DW, sprintBox, type Timeline } from "./layout";
import { aimedTrain, epicNote, shortBranch, trainLabel, urgency } from "./llb";
import type {
	AppId,
	AppVersion,
	Board,
	BoardApp,
	Day,
	Dest,
	DestKind,
	Identity,
	MineReason,
	Route,
	SprintCard,
	SprintRow,
	Train,
} from "./types";
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

export function leadKind(routes: readonly Route[]): DestKind {
	const kinds = routes.map((r) => r.kind);
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
		case "nomr":
			return "nomr";
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
		case "nomr":
			return "passive";
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
		case "nomr":
			return "∅ no code";
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
		case "nomr":
			return `no code ${n}`;
	}
}

function routeChip(b: Board, d: Dest, n: number): string {
	switch (d.kind) {
		case "off":
			return `off flow ${n}`;
		case "none":
			return `no MR ${n}`;
		case "nomr":
			return `no code ${n}`;
		default:
			return `${destLabel(b, d)} ${n}`;
	}
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

export type CardView = { me: boolean; mobile: boolean };
export type CardCount = { text: string; sub: string | null };

export function cardCount(cards: readonly SprintCard[], view: CardView): CardCount {
	if (view.me) return { text: `${plural(cards.length, "card")} of mine`, sub: null };
	if (view.mobile) return { text: plural(cards.length, "mobile card"), sub: null };
	const mobile = cards.filter((c) => c.mobile).length;
	return { text: plural(cards.length, "card"), sub: mobile < cards.length ? `${mobile} mobile` : null };
}

export function emptyCountText(cards: readonly SprintCard[], view: CardView): string {
	if (view.me) return "nothing of mine";
	if (cards.length === 0 && !view.mobile) return "no cards";
	return cards.some((c) => c.mobile) ? "no cards" : "no mobile cards";
}

export const MOBILE_RULE =
	"A card counts as mobile when it is titled App, iOS or ADR, has a mobile subtask or an MR, or is in a mobile project. Any other card shows as web: no rule, count or route reads it. A sprint that has not started has no subtasks or MRs yet.";

export type Chip = { text: string; tone: Tone };
export type BoardLabel = { title: string; dates: string; scope: string; dests: Chip[]; routes: Chip[] };

type KeptDest = { to: Dest; cards: SprintCard[] };

const byKey = (a: SprintCard, b: SprintCard) => a.key.localeCompare(b.key, undefined, { numeric: true });

function keptDests(row: SprintRow, cards: readonly SprintCard[]): KeptDest[] {
	const active = new Map(cards.filter((c) => c.mobile && c.active).map((c) => [c.key, c]));
	return row.dests
		.map((d) => ({ to: d.to, cards: d.keys.flatMap((k) => active.get(k) ?? []).sort(byKey) }))
		.filter((d) => d.cards.length > 0);
}

/** Every count comes from the cards the lens keeps, so Only me and the app filter change the label too. */
export function boardLabel(row: SprintRow, b: Board, all: readonly SprintCard[]): BoardLabel {
	const cards = all.filter((c) => c.mobile);
	const active = cards.filter((c) => c.active);
	const isNew = active.filter((c) => c.activeWhy.includes("new this sprint")).length;
	const carried = cards.length - active.length;
	const scope = [
		`${isNew} new`,
		active.length > isNew ? `${active.length - isNew} carried with MRs` : "",
		carried ? `${carried} carried, done` : "",
	].filter(Boolean);
	const dests = keptDests(row, cards);
	return {
		title: `${row.board.key} ${row.number}`,
		dates: row.estimated
			? "no dates yet"
			: `${row.sprint.start ? shortDay(row.sprint.start) : "?"} → ${row.sprint.end ? shortDay(row.sprint.end) : "?"}`,
		scope: scope.join(" · "),
		dests: dests.map((d) => ({ text: destChip(b, d.to, d.cards.length), tone: destTone(b, d.to) })),
		routes: [
			...dests.map((d) => ({ text: routeChip(b, d.to, d.cards.length), tone: destTone(b, d.to) })),
			...(carried ? [{ text: `+${carried} carried over`, tone: "todo" as const }] : []),
		],
	};
}

export type DestBlock = { key: string; to: Dest | null; cards: SprintCard[] };

export const WEB_BLOCK = "web";

export function destBlocks(row: SprintRow, cards: readonly SprintCard[]): DestBlock[] {
	const carried = cards.filter((c) => c.mobile && !c.active).sort(byKey);
	const web = cards.filter((c) => !c.mobile).sort(byKey);
	return [
		...keptDests(row, cards).map((d) => ({ key: destKey(d.to), to: d.to, cards: d.cards })),
		...(carried.length ? [{ key: "carried", to: null, cards: carried }] : []),
		...(web.length ? [{ key: WEB_BLOCK, to: null, cards: web }] : []),
	];
}

function routesTo(c: SprintCard, key: string): Route[] {
	return c.routes.filter((r) => destKey(r.to) === key);
}

export function isSplit(c: SprintCard): boolean {
	return new Set(c.routes.map((r) => destKey(r.to))).size > 1;
}

export function destTally(to: Dest | null, cards: readonly SprintCard[]): string {
	if (!to) return "";
	const key = destKey(to);
	const routes = cards.flatMap((c) => routesTo(c, key));
	const merged = routes.filter((r) => DEST_META[r.kind].tone === "done").length;
	const review = routes.filter((r) => DEST_META[r.kind].tone === "running").length;
	const split = cards.filter(isSplit).length;
	const off = cards.filter((c) => routesTo(c, key).some((r) => r.mismatch)).length;
	return [
		merged ? `${merged} MR merged` : "",
		review ? `${review} in review` : "",
		split ? `${split} split` : "",
		off ? `${off} not as planned` : "",
	]
		.filter(Boolean)
		.join(" · ");
}

/** `note` reads before the card count, `tail` after it, as d4 lays them out. */
export type DestHead = {
	to: string;
	tone: Tone;
	freeze?: Day;
	note?: string;
	tail?: string;
	target?: { text: string; tone: Tone; title?: string };
};

const READY_NOTE = (cards: readonly SprintCard[]) =>
	`${cards.filter((c) => statusTone(c.status, c.statusCategory) === "done").length} ready or deployed in Jira`;

export function destHead(b: Board, to: Dest | null, cards: readonly SprintCard[], key?: string): DestHead {
	if (!to && key === WEB_BLOCK)
		return {
			to: "· not mobile",
			tone: "passive",
			tail: "no mobile subtask, MR, title or project, so no rule reads them",
		};
	if (!to)
		return {
			to: "↺ carried over",
			tone: "todo",
			tail: `in an earlier sprint, mobile work done before this one · ${READY_NOTE(cards)}`,
		};
	const head = { to: destLabel(b, to), tone: destTone(b, to) };
	switch (to.kind) {
		case "train": {
			const t = b.trains.find((x) => x.id === to.train);
			const apps = new Set(cards.flatMap((c) => routesTo(c, destKey(to)).map((r) => r.app)));
			const versions = [...new Set((t?.versions ?? []).filter((v) => apps.has(v.app)).map((v) => v.version))];
			return { ...head, freeze: t?.freeze, note: versions.join(" / ") };
		}
		case "llb": {
			const g = b.llbs.find((x) => x.id === to.group);
			const aimed = g ? aimedTrain(g) : (to.via ?? null);
			const target =
				g?.target.kind === "landed"
					? { text: `landed in ${trainLabel(b, g.target.train)}`, tone: "done" as const }
					: aimed
						? {
								text: `then → ${trainLabel(b, aimed)}${g?.target.kind === "epic" ? " · epic" : ""}`,
								tone: g ? urgency(g, b) : ("accent" as const),
								...(g && epicNote(g, b) ? { title: epicNote(g, b) ?? undefined } : {}),
							}
						: { text: "no target train", tone: "todo" as const };
			return { ...head, target };
		}
		case "before":
			return { ...head, note: "already shipped, older than this timeline" };
		case "off":
			return { ...head, tail: "straight into develop or another branch" };
		case "none":
			return { ...head, tail: "no MR in any repo" };
		case "nomr":
			return { ...head, tail: "marked No MR needed in AO" };
	}
}

export function carriedTo(b: Board, c: SprintCard): Chip | null {
	const to = c.routes.find((r) => r.to.kind !== "off" && r.to.kind !== "none")?.to;
	if (!to) return null;
	return to.kind === "llb"
		? { text: `parked in ⑂ ${shortBranch(to.branch)}`, tone: "hotfix" }
		: { text: `went to ${destLabel(b, to).replace(/^→ /, "")}`, tone: "passive" };
}

export function sprintShipsIn(b: Board, row: SprintRow): Train | undefined {
	const ids = new RegExp(`^${row.number + 1}-\\d{4}$`);
	const found = b.trains.filter((t) => ids.test(t.id));
	const start = row.sprint.start;
	return found.find((t) => !start || !t.freeze || t.freeze >= start) ?? found[0];
}

export function sprintFoldKey(row: SprintRow): string {
	return row.next ? `sp-${row.board.key}-next` : `sp-${row.board.key}`;
}

export type BoardSprints = { key: string; rows: SprintRow[] };

export function sprintsByBoard(rows: readonly SprintRow[]): BoardSprints[] {
	const boards = new Map<string, SprintRow[]>();
	for (const r of rows) boards.set(r.board.key, [...(boards.get(r.board.key) ?? []), r]);
	return [...boards].map(([key, rs]) => ({ key, rows: rs.sort((a, b) => Number(a.next) - Number(b.next)) }));
}

export const MIN_BAR = 4 * DW;

export type BarSpan = { x0: number; width: number };

export function barSpans(rows: readonly SprintRow[], tl: Timeline): BarSpan[] {
	let edge = Number.NEGATIVE_INFINITY;
	return rows.map((r) => {
		const start = r.sprint.start ?? tl.start;
		const box = sprintBox(tl, start, r.sprint.end ?? start);
		const own = box.left + 2;
		const right = own + Math.max(MIN_BAR, box.width - 4);
		const x0 = Math.max(own, edge);
		edge = Math.max(right, x0 + MIN_BAR) + 4;
		return { x0, width: Math.max(MIN_BAR, right - x0) };
	});
}

export type Extent = { right: number; bottom: number };

export function stackTop(earlier: readonly Extent[], x0: number, base: number): number {
	return earlier.filter((e) => e.right > x0).reduce((y, e) => Math.max(y, e.bottom), base);
}

export type SprintLens = {
	me: boolean;
	apps: ReadonlySet<AppId> | null;
	hidden: ReadonlySet<string>;
	mobile: boolean;
};

export function lensSprintCards(row: SprintRow, lens: SprintLens): SprintCard[] {
	if (lens.hidden.has(row.board.key)) return [];
	return row.cards.filter(
		(c) =>
			c.status !== "Abandoned" &&
			(!lens.mobile || c.mobile) &&
			(!lens.me || (c.mine?.length ?? 0) > 0) &&
			(!lens.apps || !c.mrs.length || c.mrs.some((m) => lens.apps?.has(m.app))),
	);
}

export function hiddenBoardsText(rows: readonly SprintRow[], hidden: ReadonlySet<string>): string | null {
	const n = new Set(rows.filter((r) => hidden.has(r.board.key)).map((r) => r.board.key)).size;
	return n ? `${n} hidden` : null;
}

export type PillApp = {
	app: string;
	platform: BoardApp["platform"];
	kind: DestKind;
	tone: Tone;
	mrs: number[];
	url: string;
};
export type MrPill = { family: string; label: string; apps: PillApp[] };

/** Colour and link come from the app's route, so a pill always agrees with the row's lead glyph. */
export function mrPills(c: SprintCard, apps: readonly BoardApp[]): MrPill[] {
	const families = [...new Set(apps.map((a) => a.family))];
	return families.map((family) => ({
		family,
		label: appMark(apps.find((a) => a.family === family) ?? apps[0]).family,
		apps: apps
			.filter((a) => a.family === family)
			.flatMap((a) => {
				const mrs = c.mrs.filter((m) => m.app === a.id);
				if (!mrs.length) return [];
				const route = c.routes.find((r) => r.app === a.id);
				const lead = mrs.find((m) => m.iid === route?.mr) ?? mrs.find((m) => m.state === "merged") ?? mrs[0];
				const kind = route?.kind ?? lead.dest;
				return [
					{
						app: a.id,
						platform: a.platform,
						kind,
						tone: DEST_META[kind].tone,
						mrs: mrs.map((m) => m.iid),
						url: lead.url,
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
	no_mr: "marked by you",
};

export function mineText(mine: readonly MineReason[]): string {
	return mine.map((m) => (m.ref ? `${MINE_TEXT[m.why]} ${m.ref}` : MINE_TEXT[m.why])).join(" · ");
}

export function versionMineText(v: AppVersion, me: Identity): string {
	const mine = (v.mine ?? []).filter((m) => m.why !== "card");
	const approved =
		me.kind === "known" && v.jira?.approvers.some((a) => a.accountId === me.jiraAccountId && a.status === "APPROVED");
	return mine
		.map((m) => (m.why === "approver" ? (approved ? "approved" : "approver, pending") : mineText([m])))
		.join(" · ");
}
