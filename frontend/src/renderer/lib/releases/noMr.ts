import type { AlertHit, Board, MineReason, NoMRMark, SprintCard, SprintRow } from "./types";

export function noMrOfCard(b: Board, key: string): NoMRMark | null {
	for (const row of b.sprints) {
		const c = row.cards.find((x) => x.key === key);
		if (c?.noMr) return c.noMr;
	}
	for (const t of b.trains)
		for (const v of t.versions) for (const u of v.cards) if (u.key === key && u.code.kind === "no_mr") return u.code;
	return null;
}

export type MarkedCard = { key: string; summary: string; mark: NoMRMark };

export function markedCards(b: Board): MarkedCard[] {
	const out = new Map<string, MarkedCard>();
	for (const row of b.sprints)
		for (const c of row.cards) if (c.noMr) out.set(c.key, { key: c.key, summary: c.summary, mark: c.noMr });
	for (const t of b.trains)
		for (const v of t.versions)
			for (const u of v.cards)
				if (u.code.kind === "no_mr" && !out.has(u.key))
					out.set(u.key, { key: u.key, summary: u.summary, mark: u.code });
	return [...out.values()].sort((a, b) => a.key.localeCompare(b.key, undefined, { numeric: true }));
}

/** Only a card with no MR in any repo can be marked; a marked one can always be unmarked. */
export function canMarkNoMr(b: Board, key: string): boolean {
	if (noMrOfCard(b, key)) return true;
	for (const row of b.sprints) for (const c of row.cards) if (c.key === key && c.mrs.length) return false;
	for (const t of b.trains)
		for (const v of t.versions) for (const u of v.cards) if (u.key === key && u.code.kind !== "none") return false;
	return true;
}

const withMark = (mine: readonly MineReason[] | undefined, on: boolean): MineReason[] | undefined => {
	const rest = (mine ?? []).filter((m) => m.why !== "no_mr");
	const out = on ? [...rest, { why: "no_mr" as const }] : rest;
	return out.length ? out : undefined;
};

function retally(row: SprintRow, c: SprintCard, on: boolean): void {
	if (c.routes.length || !c.active || c.status === "Abandoned") return;
	const [from, to] = on ? (["none", "nomr"] as const) : (["nomr", "none"] as const);
	for (const d of row.dests)
		if (d.to.kind === from && d.keys.includes(c.key)) {
			d.keys = d.keys.filter((k) => k !== c.key);
			d.count = d.keys.length;
		}
	const dest = row.dests.find((d) => d.to.kind === to);
	if (dest) {
		dest.keys = [...dest.keys, c.key];
		dest.count = dest.keys.length;
	} else row.dests.push({ to: { kind: to }, count: 1, keys: [c.key] });
	row.dests = row.dests.filter((d) => d.count > 0);
}

/** Mirrors Reconcile for one mark, so a click shows its result before the next Board arrives. */
export function applyNoMr(b: Board, key: string, mark: NoMRMark | null): void {
	const on = mark !== null;
	for (const row of b.sprints)
		for (const c of row.cards) {
			if (c.key !== key) continue;
			if (mark) c.noMr = mark;
			else delete c.noMr;
			c.mine = withMark(c.mine, on);
			retally(row, c, on);
		}
	for (const t of b.trains)
		for (const v of t.versions)
			for (const u of v.cards) {
				if (u.key !== key) continue;
				if (mark && u.code.kind === "none") u.code = { kind: "no_mr", ...mark };
				else if (!mark && u.code.kind === "no_mr") u.code = { kind: "none" };
				u.mine = withMark(u.mine, u.code.kind === "no_mr");
			}
	const parked = skipped.get(b) ?? new Map<string, AlertHit[]>();
	skipped.set(b, parked);
	if (on) {
		const skips = (h: AlertHit) => h.rule === "G2" && h.subject.kind === "card" && h.subject.key === key;
		parked.set(key, [...(parked.get(key) ?? []), ...b.alerts.hits.filter(skips)]);
		b.alerts.hits = b.alerts.hits.filter((h) => !skips(h));
	} else {
		b.alerts.hits = [...b.alerts.hits, ...(parked.get(key) ?? [])];
		parked.delete(key);
	}
	b.alerts.badge = { ...b.alerts.badge, count: b.alerts.hits.filter((h) => h.bucket === "badge").length };
}

const skipped = new WeakMap<Board, Map<string, AlertHit[]>>();
