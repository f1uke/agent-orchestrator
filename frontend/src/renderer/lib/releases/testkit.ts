import type { AppVersion, Board, Hotfix, Stage, StageKey, StageStatus, Train } from "./types";

export function stage(key: StageKey, date: string | undefined, status: StageStatus, detail = ""): Stage {
	return { key, date, status, detail, reads: [] };
}

export function version(
	train: string,
	app: string,
	v: string,
	start: string,
	due: string,
	over: Partial<AppVersion> = {},
): AppVersion {
	return {
		train,
		app,
		version: v,
		milestone: { id: 1, project: 1, title: `v ${v}`, description: "", state: "active", url: "", start, due },
		stages: [],
		cards: [],
		issues: [],
		...over,
	};
}

export function train(
	id: string,
	freeze: string,
	publish: string,
	phase: Train["phase"],
	versions: AppVersion[],
	over: Partial<Train> = {},
): Train {
	return {
		id,
		label: `#${id.replace(/-\d{4}$/, "")}`,
		phase,
		freeze,
		publish,
		versions,
		families: ["demo"],
		datesSplit: [],
		pages: [],
		...over,
	};
}

export function hotfix(app: string, v: string, start: string, due: string, over: Partial<Hotfix> = {}): Hotfix {
	return {
		app,
		version: v,
		milestone: { id: 9, project: 1, title: v, description: "hotfix", state: "active", url: "", start, due },
		...over,
	};
}

export function board(over: Partial<Board> = {}): Board {
	return {
		today: "2026-10-14",
		apps: [
			{ id: "di", name: "Demo iOS", family: "demo", platform: "ios" },
			{ id: "da", name: "Demo Android", family: "demo", platform: "android" },
		],
		trains: [],
		hotfixes: [],
		sprints: [],
		llbs: [],
		mismatches: [],
		sources: [],
		kinds: [],
		cutover: null,
		identity: { kind: "unknown", reason: "not read" },
		alerts: { badge: { count: 0, unknown: 0 }, hits: [], rules: [], staleHides: [] },
		catalogue: [],
		...over,
	};
}
