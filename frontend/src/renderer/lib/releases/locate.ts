import { branchFoldKey, cardGroups } from "./cards";
import { timeline } from "./layout";
import { llbGroupsFor } from "./llb";
import { destKey, lensSprintCards, sprintFoldKey } from "./sprint";
import type { AppId, Board, Subject, SubjectKind, TrainKey } from "./types";
import type { Layer, Layers } from "./url";

export type Target =
	| "version-bar"
	| "stage-marker"
	| "hotfix-bar"
	| "card-row"
	| "sprint-card"
	| "mr-glyph"
	| "llb-lane"
	| "app-label"
	| "train-chip"
	| "sprint-band"
	| "sheet-part";

export type Reveal = {
	target: Target;
	selectors: readonly string[];
	layers: readonly Layer[];
	unhide: readonly string[];
	folds: readonly string[];
	focus: TrainKey | null;
	hotfixes: boolean;
	/** Apps the app filter must show for the target's row to exist. */
	apps: readonly AppId[];
	/** Only me must go off: the target is not mine, so Only me does not draw it. */
	notMine: boolean;
	/** Mobile only must go off: the target is not a mobile card, so Mobile only does not draw it. */
	notMobile: boolean;
	/** Where Show lands when the subject has no mark of its own on the timeline, for the row to say. */
	fallback: string | null;
};

export type RevealView = {
	layers: Layers;
	hiddenBoards: ReadonlySet<string>;
	apps: ReadonlySet<AppId> | null;
	focus: TrainKey | null;
	me: boolean;
	mobile: boolean;
};

const css = (v: string) => JSON.stringify(v);

function plan(target: Target, selectors: readonly string[], over: Partial<Reveal> = {}): Reveal {
	return {
		target,
		selectors,
		layers: [],
		unhide: [],
		folds: [],
		focus: null,
		hotfixes: false,
		apps: [],
		notMine: false,
		notMobile: false,
		fallback: null,
		...over,
	};
}

function appLabel(app: AppId, fallback: string): Reveal {
	return plan("app-label", [`[data-testid=${css(`label-${app}`)}]`], { apps: [app], fallback });
}

function trainChip(train: TrainKey, fallback: string): Reveal {
	return plan("train-chip", [`.rel-tchip[data-train=${css(train)}]`], { focus: train, fallback });
}

function bar(b: Board, train: TrainKey, app: AppId, over: Partial<Reveal> = {}): Reveal {
	const t = b.trains.find((x) => x.id === train);
	const v = t?.versions.find((x) => x.app === app);
	if (!v) return appLabel(app, "at its app: its train has no milestone for this app");
	const publish = v.milestone.due ?? t?.publish;
	if (!publish || publish < timeline(b).start) return appLabel(app, "at its app: its train is older than the timeline");
	return plan("version-bar", [`[data-testid=${css(`bar-${app}-${train}`)}]`], { apps: [app], ...over });
}

function stageMarker(b: Board, train: TrainKey, app: AppId, stage: "freeze" | "publish", view: RevealView): Reveal {
	const shown = view.layers.past || view.focus === train;
	const r = bar(b, train, app);
	if (r.target !== "version-bar") return r;
	return {
		...r,
		target: "stage-marker",
		selectors: [`.rel-app[data-app=${css(app)}] .rel-mk[data-key=${css(train)}][data-stage=${css(stage)}]`],
		focus: shown ? null : train,
	};
}

function hotfix(b: Board, app: AppId, version: string, view: RevealView): Reveal {
	const h = b.hotfixes.find((x) => x.app === app && x.version === version);
	return h
		? plan("hotfix-bar", [`[data-testid=${css(`hotfix-${app}-${version}`)}]`], {
				hotfixes: true,
				apps: [app],
				notMine: view.me && !h.mine?.length,
			})
		: appLabel(app, "at its app: the hotfix is no longer on the Board");
}

function cardOnTrain(b: Board, train: TrainKey, app: AppId, key: string): Reveal {
	const g = cardGroups(b, app).find((x) => x.train.id === train);
	if (!g) return bar(b, train, app, { fallback: "at its train's bar: a published train lists no cards" });
	const row = (folds: string[]) =>
		plan("card-row", [`[data-testid=${css(`cards-${g.key}`)}] .crow[data-key=${css(key)}]`], {
			layers: ["cards"],
			apps: [app],
			folds,
		});
	if (g.direct.some((u) => u.key === key)) return row([g.key]);
	const via = g.llbs.find((l) => l.cards.some((c) => c.key === key));
	if (via) return row([g.key, branchFoldKey(g, via.branch)]);
	return bar(b, train, app, { fallback: "at its train's bar: the card is not in the train's card list" });
}

function sprintCard(b: Board, key: string, view: RevealView, app?: AppId): Reveal | null {
	for (const row of b.sprints) {
		const card = row.cards.find((c) => c.key === key);
		if (!card) continue;
		const kept = lensSprintCards(row, { me: false, apps: view.apps, hidden: new Set(), mobile: false }).some(
			(c) => c.key === key,
		);
		const apps = kept ? [] : [app ?? card.mrs[0]?.app].filter((a): a is AppId => !!a);
		const route = (app && card.routes.find((r) => r.app === app)) || card.routes[0];
		const block = !card.mobile ? "web" : !card.active ? "carried" : route ? destKey(route.to) : "none";
		const fk = sprintFoldKey(row);
		const rowSel = `[data-testid=${css(`sprint-row-${row.board.key}`)}] [data-sprint=${css(row.next ? "next" : "active")}][data-dest=${css(block)}] .crow[data-key=${css(key)}]`;
		return plan(
			app ? "mr-glyph" : "sprint-card",
			[app ? `${rowSel} [data-testid=mr-glyph][data-app=${css(app)}]` : rowSel],
			{
				layers: ["sprint"],
				unhide: view.hiddenBoards.has(row.board.key) ? [row.board.key] : [],
				apps,
				notMine: view.me && !card.mine?.length,
				notMobile: view.mobile && !card.mobile,
				folds: [fk, `${fk}:${block}`],
			},
		);
	}
	return null;
}

function mr(s: Extract<Subject, { kind: "mr" }>, b: Board, view: RevealView): Reveal {
	for (const g of llbGroupsFor(b, s.app)) {
		const br = g.branches.find((x) => x.app === s.app && x.branch === s.source);
		if (br)
			return plan(
				"llb-lane",
				[`.rel-app[data-app=${css(s.app)}] [data-testid=llb-lane][data-branch=${css(br.branch)}]`],
				{
					layers: ["llb"],
					apps: [s.app],
					notMine: view.me && !g.mine?.length,
					folds: [`${s.app}:llbs`],
				},
			);
	}
	for (const row of b.sprints) {
		const card = row.cards.find((c) => c.mrs.some((m) => m.app === s.app && m.iid === s.iid));
		const r = card && sprintCard(b, card.key, view, s.app);
		if (r) return r;
	}
	return appLabel(s.app, `at its app: the timeline draws no lane for ${s.target}`);
}

type Locator<K extends SubjectKind> = (s: Extract<Subject, { kind: K }>, b: Board, view: RevealView) => Reveal;

export const LOCATORS: { [K in SubjectKind]: Locator<K> } = {
	version: (s, b) => bar(b, s.train, s.app),
	versionDate: (s, b, view) =>
		s.field === "page" ? bar(b, s.train, s.app) : stageMarker(b, s.train, s.app, s.field, view),
	jiraVersion: (s) => appLabel(s.app, "at its app: a Jira release on no train has no mark on the timeline"),
	milestone: (s) => appLabel(s.app, "at its app: a milestone with no Jira release has no mark on the timeline"),
	hotfix: (s, b, view) => hotfix(b, s.app, s.version, view),
	hotfixTrain: (s, b, view) => hotfix(b, s.app, s.version, view),
	trainPair: (s) => trainChip(s.b, "at the later train: two trains have no single mark"),
	cardOnTrain: (s, b) => cardOnTrain(b, s.train, s.app, s.key),
	card: (s, b, view) =>
		sprintCard(b, s.key, view) ??
		plan("sprint-band", [".rel-spr"], { fallback: "at the sprint: the card is on no board's row" }),
	mr,
	pageFamily: (s, b) => {
		const apps = (b.trains.find((x) => x.id === s.train)?.versions ?? [])
			.map((v) => v.app)
			.filter((app) => b.apps.find((a) => a.id === app)?.family === s.family);
		if (!apps.length) return trainChip(s.train, "at the train: no app of the family is on it");
		const tries = apps.flatMap((app) => s.keys.map((key) => cardOnTrain(b, s.train, app, key)));
		const rows = tries.filter((r) => r.target === "card-row");
		if (!rows.length) return tries[0] ?? bar(b, s.train, apps[0]);
		const all = <T>(pick: (r: Reveal) => readonly T[]) => [...new Set(rows.flatMap(pick))];
		return plan(
			"card-row",
			all((r) => r.selectors),
			{
				layers: ["cards"],
				apps: all((r) => r.apps),
				folds: all((r) => r.folds),
			},
		);
	},
};

export function sheetReveal(selector: string): Reveal {
	return plan("sheet-part", [`.rel-sheet :is(${selector})`]);
}

export function locate(s: Subject, b: Board, view: RevealView): Reveal {
	return (LOCATORS[s.kind] as Locator<Subject["kind"]>)(s as never, b, view);
}
