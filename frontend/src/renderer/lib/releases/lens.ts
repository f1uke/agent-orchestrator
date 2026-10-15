import type {
	AlertHit,
	AppId,
	AppVersion,
	Badge,
	Board,
	BoardApp,
	CardUnit,
	Hotfix,
	Identity,
	LLBGroup,
	MineReason,
	SprintCard,
	SprintRow,
	ResourceStatus,
	SourceFreshness,
	Stage,
	StageStatus,
	Train,
	TrainKey,
} from "./types";
import type { ReleasesSearch } from "./url";
import { SEVERITY_META, stageTone, type Tone } from "./vocab";

export function nowStage(stages: Stage[]): Stage | null {
	return stages.find((s) => s.status !== "done" && s.status !== "not_observable") ?? null;
}

const TONE_RANK: readonly StageStatus[] = ["failed", "late", "unknown", "waiting", "running", "todo", "done"];

export function worstTone(stages: Stage[]): Tone {
	const seen: StageStatus[] = stages.map((s) => s.status).filter((s) => s !== "not_observable");
	return stageTone(TONE_RANK.find((s) => seen.includes(s)) ?? "done");
}

export function unknownText(missing: ResourceStatus[] | undefined, now: number): string {
	const m = missing?.[0];
	if (!m) return "unknown";
	const what = m.errorClass ? `${m.kind} ${m.errorClass}` : m.kind;
	return m.completeAt
		? `unknown: ${what}, last read ${ageText(now - Date.parse(m.completeAt))} ago`
		: `unknown: ${what} never read`;
}

export function badgeLabel(b: Badge): string {
	if (b.count === 0 && b.unknown > 0) return `? ${b.unknown} unknown`;
	return b.unknown > 0 ? `${b.count} · ${b.unknown} unknown` : String(b.count);
}

export function focusTrain(b: Board, key: TrainKey | null): Train | null {
	return b.trains.find((t) => t.id === key) ?? b.trains.find((t) => t.phase === "current") ?? b.trains.at(-1) ?? null;
}

export function trainChips(b: Board, history: boolean, focus: TrainKey | null): Train[] {
	if (history) return b.trains;
	const lastPast = b.trains.filter((t) => t.phase === "past").at(-1);
	return b.trains.filter((t) => t.phase !== "past" || t === lastPast || t.id === focus);
}

export function visibleApps(b: Board, apps: ReadonlySet<AppId> | null): BoardApp[] {
	return apps ? b.apps.filter((a) => apps.has(a.id)) : b.apps;
}

function shown(apps: ReadonlySet<AppId> | null) {
	return (app: AppId | undefined) => !apps || !app || apps.has(app);
}

function bySeverity(a: AlertHit, b: AlertHit): number {
	return SEVERITY_META[a.severity].rank - SEVERITY_META[b.severity].rank;
}

function isMine(x: { mine?: readonly MineReason[] }): boolean {
	return (x.mine?.length ?? 0) > 0;
}

/** Only me counts no Unknown cell: a rule that could not read cannot say whose its hits would be. */
export function lensBadge(b: Board, apps: ReadonlySet<AppId> | null, me = false): Badge {
	if (!apps && !me) return b.alerts.badge;
	const on = shown(apps);
	const hits = b.alerts.hits.filter((h) => h.bucket === "badge" && on(h.app) && (!me || isMine(h))).sort(bySeverity);
	const unknown = me ? 0 : b.alerts.rules.filter((r) => r.on && r.outcome.kind === "unknown" && on(r.app)).length;
	return { count: hits.length, unknown, worst: hits[0]?.severity };
}

export type Lens = {
	focus: Train | null;
	apps: BoardApp[];
	badge: Badge;
	attention: AlertHit[];
	history: AlertHit[];
	hidden: AlertHit[];
	appHits: (app: AppId) => AlertHit[];
	/** Only me is on and the identity is known; with an unknown identity ?me=1 filters nothing. */
	me: boolean;
	identity: Identity;
	keepVersion: (v: AppVersion) => boolean;
	keepCard: (u: CardUnit) => boolean;
	keepHotfix: (h: Hotfix) => boolean;
	sprintCards: (row: SprintRow) => SprintCard[];
	llbs: LLBGroup[];
};

export function makeLens(b: Board, url: ReleasesSearch): Lens {
	const on = shown(url.apps);
	const me = url.me && b.identity.kind === "known";
	const keep = <T extends { mine?: readonly MineReason[] }>(x: T) => !me || isMine(x);
	const focus = focusTrain(b, url.focus);
	const bucket = (k: AlertHit["bucket"]) =>
		b.alerts.hits.filter((h) => h.bucket === k && on(h.app) && keep(h)).sort(bySeverity);
	const attention = bucket("badge");
	return {
		focus,
		apps: visibleApps(b, url.apps),
		badge: lensBadge(b, url.apps, me),
		attention,
		history: bucket("history"),
		hidden: bucket("hidden"),
		appHits: (app) => attention.filter((h) => h.app === app && h.train === focus?.id),
		me,
		identity: b.identity,
		keepVersion: keep,
		keepCard: keep,
		keepHotfix: keep,
		sprintCards: (row) =>
			row.cards.filter((c) => c.status !== "Abandoned" && keep(c) && (!c.mrs.length || c.mrs.some((m) => on(m.app)))),
		llbs: b.llbs.filter(keep),
	};
}

export function versionOf(t: Train, app: AppId): AppVersion | undefined {
	return t.versions.find((v) => v.app === app);
}

export type Owner = { name: string; from: "release MR author" | "release ticket assignee" };

export function owner(v: AppVersion): Owner | null {
	if (v.releaseMr) return { name: v.releaseMr.mr.author, from: "release MR author" };
	if (v.ticket?.assignee) return { name: v.ticket.assignee.name, from: "release ticket assignee" };
	return null;
}

export function approvals(v: AppVersion | undefined): { ok: number; total: number } {
	const list = v?.jira?.approvers ?? [];
	return { ok: list.filter((a) => a.status === "APPROVED").length, total: list.length };
}

export type TrainApprover = {
	accountId: string;
	name: string;
	pending: boolean;
	apps: { app: AppId; approved: boolean }[];
};

export function trainApprovers(t: Train, apps: readonly AppId[]): TrainApprover[] {
	const people = new Map<string, TrainApprover>();
	for (const app of apps) {
		for (const a of versionOf(t, app)?.jira?.approvers ?? []) {
			const p = people.get(a.accountId) ?? { accountId: a.accountId, name: a.name, pending: false, apps: [] };
			p.apps.push({ app, approved: a.status === "APPROVED" });
			p.pending ||= a.status !== "APPROVED";
			people.set(a.accountId, p);
		}
	}
	return [...people.values()].sort((x, y) => Number(y.pending) - Number(x.pending) || x.name.localeCompare(y.name));
}

export function ageText(ms: number): string {
	const min = Math.floor(ms / 60_000);
	if (min < 1) return "now";
	if (min < 60) return `${min}m`;
	const h = Math.floor(min / 60);
	return h < 48 ? `${h}h` : `${Math.floor(h / 24)}d`;
}

export function agoText(ms: number): string {
	const age = ageText(ms);
	return age === "now" ? "just now" : `${age} ago`;
}

export type SourceAge = { system: string; age: string; unknown: boolean; why: string };

/** fetchError: the Board itself could not be refetched, so every age is the last one known. */
export function sourceAges(sources: SourceFreshness[], now: number, fetchError: string | null): SourceAge[] {
	return sources.map((s) => {
		const age = s.asOf ? ageText(now - Date.parse(s.asOf)) : "never read";
		if (fetchError) return { system: s.system, age, unknown: true, why: fetchError };
		if (!s.asOf) return { system: s.system, age, unknown: true, why: "never read" };
		if (s.issues.length === 0) return { system: s.system, age, unknown: false, why: "" };
		const kinds = [...new Set(s.issues.map((i) => (i.errorClass ? `${i.kind} ${i.errorClass}` : i.kind)))];
		return {
			system: s.system,
			age,
			unknown: true,
			why: `${s.issues.length} of ${s.resources} not fresh: ${kinds.join(", ")}`,
		};
	});
}
