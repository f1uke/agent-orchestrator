import { sourceAges, type SourceAge } from "./lens";
import type { Board, CardUnit, LLBGroup, RuleInfo, SprintCard, SprintRow, Stage, StageKey, TrainKey } from "./types";
import { CODE_META, STAGE_META } from "./vocab";

export type KindName = string;
type CardWhy = CardUnit["why"][number];

export type SourceLine = { kind: KindName; text?: string; on?: boolean; short?: string };

export type Provenance = {
	title: string;
	how: "read" | "derived" | "merged" | "rule";
	lines: SourceLine[];
	sub?: string;
	note?: string;
	field?: string;
	rule?: string;
};

export type SystemMeta = { name: string; short: string; mark: string; besideOthers: boolean };

export const SYSTEMS: Readonly<Record<string, SystemMeta>> = {
	jira: { name: "Jira", short: "Jira", mark: "J", besideOthers: true },
	gitlab: { name: "GitLab", short: "GitLab", mark: "G", besideOthers: true },
	confluence: { name: "Confluence", short: "Confluence", mark: "C", besideOthers: true },
	plan: { name: "AO plan", short: "AO plan", mark: "P", besideOthers: true },
	settings: { name: "AO settings", short: "Settings", mark: "S", besideOthers: false },
	ao: { name: "AO", short: "AO", mark: "AO", besideOthers: false },
};

export function systemMeta(system: string): SystemMeta {
	return SYSTEMS[system] ?? { name: system, short: system, mark: system.slice(0, 1).toUpperCase(), besideOthers: true };
}

/** Kinds AO itself owns; Board.kinds lists only the kinds the poller reads. */
const AO_KINDS: Readonly<Record<KindName, { system: string; label: string }>> = {
	"ao.plans": { system: "plan", label: "long-lived plans" },
	"ao.config": { system: "settings", label: "release-train.json" },
	"ao.convention": { system: "ao", label: "AO convention" },
	"ao.today": { system: "ao", label: "today's date" },
};

export type SourceIndex = {
	systemOf: (kind: KindName) => string;
	labelOf: (kind: KindName) => string;
	kindStale: (kind: KindName) => boolean;
	read: (system: string) => SourceAge | null;
	systems: SourceAge[];
	kindsOf: (system: string) => KindName[];
};

export function sourceIndex(b: Board, now: number, fetchError: string | null): SourceIndex {
	const kinds = new Map(b.kinds.map((k) => [k.kind, k]));
	const systems = sourceAges(b.sources, now, fetchError);
	const bySystem = new Map(systems.map((s) => [s.system, s]));
	const systemOf = (kind: KindName) => kinds.get(kind)?.system ?? AO_KINDS[kind]?.system ?? kind.split(".")[0];
	return {
		systemOf,
		labelOf: (kind) => kinds.get(kind)?.label ?? AO_KINDS[kind]?.label ?? kind,
		kindStale: (kind) => {
			const sys = bySystem.get(systemOf(kind));
			if (!sys) return false;
			if (fetchError) return true;
			const k = kinds.get(kind);
			return k ? !k.asOf || k.issues.length > 0 : sys.unknown;
		},
		read: (system) => bySystem.get(system) ?? null,
		systems,
		kindsOf: (system) => b.kinds.filter((k) => k.system === system).map((k) => k.kind),
	};
}

function counted(p: Provenance): SourceLine[] {
	return p.how === "merged" ? p.lines.filter((l) => l.on) : p.lines;
}

export function tagSystems(p: Provenance, idx: SourceIndex): string[] {
	const all = [...new Set(counted(p).map((l) => idx.systemOf(l.kind)))];
	return p.how === "merged" ? all : all.filter((s) => systemMeta(s).besideOthers);
}

export function tagText(p: Provenance, wide: boolean, idx: SourceIndex): string {
	const names = tagSystems(p, idx).map((s) => systemMeta(s).short);
	if (p.how === "rule") return wide ? [p.rule, names.join(" + ")].filter(Boolean).join(" · ") : `rule ${p.rule}`;
	if (p.how === "merged") {
		const on = p.lines.filter((l) => l.on);
		return on.length ? on.map((l) => l.short ?? idx.labelOf(l.kind)).join(" + ") : "no match";
	}
	return names.join(" + ") || "AO";
}

export function tagStale(p: Provenance, idx: SourceIndex): boolean {
	return counted(p).some((l) => idx.kindStale(l.kind));
}

const WHY_ORDER: readonly CardWhy[] = ["fixVersion", "release train page", "sprint branch"];
const WHY_SHORT: Readonly<Record<CardWhy, string>> = {
	fixVersion: "fixVersion",
	"release train page": "page",
	"sprint branch": "sprint MR",
};

const mrList = (iids: readonly number[]) => iids.map((n) => `!${n}`).join(" ");

export function membershipOf(u: CardUnit, train: string): Provenance {
	const e = u.evidence;
	const on = (w: CardWhy) => u.why.includes(w);
	const text: Record<CardWhy, string> = {
		fixVersion: `fixVersion is ${e.fixVersion?.name ?? `the ${train} release`}`,
		"release train page": e.page ? `listed on "${e.page.title}"` : `listed on the ${train} Release train page`,
		"sprint branch": e.sprintMR
			? `${mrList(e.sprintMR.mrs)} into ${e.sprintMR.branch}`
			: "an MR into the sprint branch",
	};
	const kind: Record<CardWhy, KindName> = {
		fixVersion: "jira.cards",
		"release train page": "confluence.pages",
		"sprint branch": "gitlab.mrs",
	};
	return {
		title: `${u.key} rides ${train}`,
		how: "merged",
		lines: WHY_ORDER.map((w) => ({ kind: kind[w], text: text[w], on: on(w), short: WHY_SHORT[w] })),
		note: "AO counts a card in a train when any one of these matches.",
		field: "CardUnit.why, CardUnit.evidence",
	};
}

export function planSaysText(planFrom: readonly CardWhy[] | undefined): string {
	const on = WHY_ORDER.filter((w) => planFrom?.includes(w));
	return on.length ? on.map((w) => WHY_SHORT[w]).join(" + ") : "page";
}

export function codeStateOf(u: CardUnit): Provenance {
	const c = u.code;
	const text =
		c.kind === "merged"
			? `${mrList(c.mrs)} merged${c.via ? ` via ${c.via}` : ""}`
			: c.kind === "open"
				? `${mrList(c.mrs)} open`
				: c.kind === "parked"
					? `${mrList(c.mrs)} into ${c.branch}`
					: c.kind === "elsewhere"
						? `merged into ${c.branch} on ${c.on}`
						: "no MR names this card";
	return {
		title: `Code: ${CODE_META[c.kind].label}`,
		how: "derived",
		lines: [{ kind: "gitlab.mrs", text }],
		note: "From the MRs whose title or source branch names the card.",
		field: "CardUnit.code",
	};
}

const CONVENTION: Partial<Record<StageKey, string>> = {
	scan: "freeze + 1 day",
	approve: "freeze + 2 days (the bot sets approvers then)",
	tag: "due - 1 day until the tag exists",
};

export function stageOf(s: Stage): Provenance {
	const conv = s.conventional ? CONVENTION[s.key] : undefined;
	return {
		title: `${STAGE_META[s.key].label} stage`,
		how: "derived",
		lines: [...s.reads.map((kind) => ({ kind })), ...(conv ? [{ kind: "ao.convention", text: conv }] : [])],
		note: conv ? "Its date is an AO convention, not one a system records." : undefined,
		field: "Stage.reads",
	};
}

export function ruleTagOf(catalogue: readonly RuleInfo[], id: string): Provenance | null {
	const r = catalogue.find((x) => x.id === id);
	return r ? ruleOf(r) : null;
}

export function ruleOf(r: RuleInfo): Provenance {
	return {
		title: `Rule ${r.id}`,
		sub: r.title,
		how: "rule",
		rule: r.id,
		lines: r.reads.map((kind) => ({ kind })),
		note: "AO checks every rule on each refresh. Turn rules off in Alert rules.",
		field: "RuleInfo.reads",
	};
}

export function llbTargetOf(g: LLBGroup, label: (train: TrainKey) => string): Provenance {
	const t = g.target;
	const p = g.plan;
	const planned = !!p && (p.targetKind === "dropped" || (p.targetKind === "train" && !!p.targetTrain));
	return {
		title: t.kind === "none" ? "No target train" : t.kind === "dropped" ? "Dropped" : `Target ${label(t.train)}`,
		how: "merged",
		lines: [
			{
				kind: "gitlab.mrs",
				short: "landed",
				on: t.kind === "landed",
				text: t.kind === "landed" ? `${t.branch} landed, rides ${label(t.train)}` : "not merged out on every branch",
			},
			{
				kind: "ao.plans",
				short: "plan",
				on: t.kind === "planned" || t.kind === "dropped",
				text: !planned
					? "no plan names a train"
					: p.targetKind === "dropped"
						? "plan says dropped"
						: `plan says ${label(p.targetTrain ?? "")}${p.note ? ` · "${p.note}"` : ""}`,
			},
			{
				kind: "confluence.pages",
				short: "epic",
				on: t.kind === "epic",
				text:
					t.kind === "epic"
						? `epic ${t.epic} is listed on the ${label(t.train)} page (${t.covered} of ${t.total} cards)`
						: "no train page lists the branch's epic",
			},
		],
		note: "First match wins: landed, then the plan, then an epic a train page lists.",
		field: "LLBGroup.target",
	};
}

export function sprintCardOf(c: SprintCard, row: SprintRow): Provenance {
	const sp = c.mrs.filter((m) => m.dest === "sprint" || m.dest === "sprint_open");
	return {
		title: `${c.key} in the ${row.board.key} row`,
		how: "merged",
		lines: [
			{
				kind: "jira.sprint_cards",
				short: `${row.board.key} board`,
				on: c.onBoard,
				text: `in the active sprint on the ${row.board.key} board`,
			},
			{
				kind: "gitlab.mrs",
				short: "sprint MR",
				on: !c.onBoard && sp.length > 0,
				text: sp.length ? `${mrList(sp.map((m) => m.iid))} into ${sp[0].target}` : "no MR into the sprint branch",
			},
		],
		note: c.onBoard
			? undefined
			: "Not on any followed board's sprint. It shows because its MR goes into the sprint branch.",
		field: "SprintCard.onBoard",
	};
}

export function readOf(title: string, kind: KindName, text?: string, field?: string): Provenance {
	return { title, how: "read", lines: [{ kind, text }], field };
}

export function derivedOf(title: string, lines: SourceLine[], note?: string, field?: string): Provenance {
	return { title, how: "derived", lines, note, field };
}
