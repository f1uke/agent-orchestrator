import { addDays } from "./day";
import type { Board, Day, LLBGroup, LlbPlanInput, PlanAccepted, PlanBranch, TrainKey } from "./types";

export type PlanDraft = {
	name: string;
	targetKind: LlbPlanInput["targetKind"];
	targetTrain?: TrainKey;
	note: string;
	branches: PlanBranch[];
	accepted: PlanAccepted[];
	ifVersion: number;
};

export function draftFrom(g: LLBGroup): PlanDraft {
	const p = g.plan;
	const branches = p?.branches.length ? p.branches : g.branches.map((b) => ({ app: b.app, branch: b.branch }));
	return {
		name: p?.name ?? g.name,
		targetKind: p?.targetKind ?? "unplanned",
		...(p?.targetTrain ? { targetTrain: p.targetTrain } : {}),
		note: p?.note ?? "",
		branches: branches.map((b) => ({ ...b })),
		accepted: (p?.accepted ?? []).map((a) => ({ ...a })),
		ifVersion: p?.version ?? 0,
	};
}

export function draftDirty(g: LLBGroup, d: PlanDraft): boolean {
	const o = draftFrom(g);
	const same = <T>(a: T[], b: T[]) => JSON.stringify(a) === JSON.stringify(b);
	return (
		o.name !== d.name ||
		o.targetKind !== d.targetKind ||
		(o.targetTrain ?? null) !== (d.targetTrain ?? null) ||
		o.note !== d.note ||
		!same(o.branches, d.branches) ||
		!same(o.accepted, d.accepted)
	);
}

export function planProblems(d: PlanDraft): string[] {
	const out: string[] = [];
	if (!d.name.trim()) out.push("Give the plan a name.");
	if (d.targetKind === "train" && !d.targetTrain) out.push("Pick a target train.");
	const seen = new Set<string>();
	for (const b of d.branches) {
		const ref = `${b.app}:${b.branch}`;
		if (!b.app || !b.branch.trim()) out.push("Every branch needs an app and a name.");
		else if (seen.has(ref)) out.push(`${ref} is listed twice.`);
		seen.add(ref);
	}
	if (d.accepted.some((a) => !a.mrRef.trim() || !a.reason.trim())) out.push("Every accepted MR needs a reason.");
	return out;
}

export function planInput(d: PlanDraft): LlbPlanInput {
	return {
		name: d.name.trim(),
		targetKind: d.targetKind,
		...(d.targetKind === "train" && d.targetTrain ? { targetTrain: d.targetTrain } : {}),
		note: d.note,
		branches: d.branches,
		accepted: d.accepted,
		ifVersion: d.ifVersion,
		via: "app",
	};
}

/** The preview's stand-in for the daemon's precedence: landed, a plan's train or dropped, the epic, none. */
export function targetAfterPlan(
	g: LLBGroup,
	p: { targetKind: LlbPlanInput["targetKind"]; targetTrain?: TrainKey },
): LLBGroup["target"] {
	if (g.target.kind === "landed") return g.target;
	if (p.targetKind === "train" && p.targetTrain) return { kind: "planned", train: p.targetTrain };
	if (p.targetKind === "dropped") return { kind: "dropped" };
	return g.target.kind === "epic" ? g.target : { kind: "none" };
}

export class PlanConflictError extends Error {
	constructor(readonly current: number) {
		super("This plan changed elsewhere. Reload it to see the change; your edit is kept here.");
		this.name = "PlanConflictError";
	}
}

type TargetChoice = { key: TrainKey; label: string; freeze: Day | undefined; guess: boolean };

const TRAIN_EVERY_DAYS = 14;

export function targetChoices(b: Board): TargetChoice[] {
	const live = b.trains.filter((t) => !t.publish || t.publish >= b.today);
	const out: TargetChoice[] = live.map((t) => ({ key: t.id, label: t.label, freeze: t.freeze, guess: false }));
	const last = [...b.trains].reverse().find((t) => /^\d+-\d{4}$/.test(t.id));
	if (!last) return out;
	const [n, year] = last.id.split("-").map(Number);
	for (let k = 1; k <= 2; k++) {
		out.push({
			key: `${n + k}-${year}`,
			label: `#${n + k}`,
			freeze: last.freeze ? addDays(last.freeze, TRAIN_EVERY_DAYS * k) : undefined,
			guess: true,
		});
	}
	return out;
}
