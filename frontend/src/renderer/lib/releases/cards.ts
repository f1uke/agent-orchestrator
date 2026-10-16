import type { AppId, Board, CardUnit, CodeState, LLBGroup, LongLivedBranch, MineReason, Person, Train } from "./types";
import type { Tone } from "./vocab";
import { aimedTrain } from "./llb";

const READY = new Set(["ready for uat", "uat approved", "ready to deploy", "ready to deliver", "deployed", "done"]);
const CATEGORY_TONE: Readonly<Record<string, Tone>> = { done: "done", indeterminate: "running", new: "todo" };

export function statusTone(status: string | undefined, category: string | undefined): Tone {
	if (READY.has((status ?? "").toLowerCase())) return "done";
	return CATEGORY_TONE[category ?? ""] ?? "todo";
}

type CodeTally = Record<CodeState["kind"], number>;

export type BranchCard = {
	key: string;
	summary?: string;
	status?: string;
	statusCategory?: string;
	assignee?: Person;
	mine?: MineReason[];
	pending: boolean;
};

type BranchInTrain = { group: LLBGroup; branch: LongLivedBranch; cards: BranchCard[] };

export type CardGroup = {
	key: string;
	app: AppId;
	train: Train;
	units: CardUnit[];
	direct: CardUnit[];
	llbs: BranchInTrain[];
	tally: CodeTally;
	ready: number;
};

export function branchFoldKey(g: CardGroup, b: LongLivedBranch): string {
	return `${g.key}:llb:${b.branch}`;
}

function ridesTrain(g: LLBGroup, b: LongLivedBranch, train: string): boolean {
	if (b.landing?.mergedOn) return b.train === train;
	return aimedTrain(g) === train;
}

function branchesForTrain(board: Board, app: AppId, train: string): { group: LLBGroup; branch: LongLivedBranch }[] {
	return board.llbs.flatMap((group) =>
		group.branches.filter((b) => b.app === app && ridesTrain(group, b, train)).map((branch) => ({ group, branch })),
	);
}

function throughBranch(u: CardUnit, names: ReadonlySet<string>): string | null {
	if (u.code.kind === "parked" && names.has(u.code.branch)) return u.code.branch;
	if (u.code.kind === "merged" && u.code.via && names.has(u.code.via)) return u.code.via;
	return null;
}

function uniqueReasons(list: MineReason[]): MineReason[] {
	return [...new Map(list.map((m) => [`${m.why}:${m.ref ?? ""}`, m])).values()];
}

function tally(units: CardUnit[]): CodeTally {
	const t: CodeTally = { merged: 0, open: 0, parked: 0, elsewhere: 0, none: 0 };
	for (const u of units) t[u.code.kind]++;
	return t;
}

export function cardGroups(board: Board, app: AppId): CardGroup[] {
	return board.trains.flatMap((train) => {
		if (train.phase === "past") return [];
		const units = train.versions.find((v) => v.app === app)?.cards ?? [];
		if (!units.length) return [];
		const rides = branchesForTrain(board, app, train.id);
		const names = new Set(rides.map((r) => r.branch.branch));
		const inBranch = new Map<string, CardUnit[]>();
		const direct: CardUnit[] = [];
		for (const u of units) {
			const name =
				throughBranch(u, names) ?? rides.find((r) => r.branch.cards.some((c) => c.key === u.key))?.branch.branch;
			if (name) inBranch.set(name, [...(inBranch.get(name) ?? []), u]);
			else direct.push(u);
		}
		const llbs = rides.map(({ group, branch }) => {
			const parked = inBranch.get(branch.branch) ?? [];
			const cards = new Map<string, BranchCard>();
			for (const c of branch.cards) if (!cards.has(c.key)) cards.set(c.key, { ...c, pending: c.mr.state === "opened" });
			for (const u of parked) if (!cards.has(u.key)) cards.set(u.key, { ...u, pending: u.code.kind === "open" });
			const mineOf = (key: string) =>
				uniqueReasons([...branch.cards, ...parked].filter((x) => x.key === key).flatMap((x) => x.mine ?? []));
			return { group, branch, cards: [...cards.values()].map((c) => ({ ...c, mine: mineOf(c.key) })) };
		});
		return [
			{
				key: `${app}:${train.id}`,
				app,
				train,
				units,
				direct,
				llbs,
				tally: tally(units),
				ready: units.filter((u) => statusTone(u.status, u.statusCategory) === "done").length,
			},
		];
	});
}

export function groupRowCount(g: CardGroup, isOpen: (key: string) => boolean): number {
	return (
		g.direct.length + g.llbs.reduce((n, l) => n + 1 + (isOpen(branchFoldKey(g, l.branch)) ? l.cards.length : 0), 0)
	);
}

/** Jira links on the Board share one host; a long-lived card carries no URL of its own. */
export function jiraUrl(board: Board, key: string): string | undefined {
	const sample = board.trains
		.flatMap((t) => t.versions.flatMap((v) => v.cards))
		.find((u) => u.url.includes("/browse/"));
	return sample?.url.replace(/\/browse\/.*$/, `/browse/${key}`);
}
