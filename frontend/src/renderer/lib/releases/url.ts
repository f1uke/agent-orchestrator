import type { AppId, HitId, TrainKey } from "./types";

export type SheetEntry =
	| { kind: "train"; train: TrainKey }
	| { kind: "version"; train: TrainKey; app: AppId }
	| { kind: "hotfix"; app: AppId; version: string }
	| { kind: "mismatch"; hit: HitId }
	| { kind: "card"; key: string }
	| { kind: "sprint"; board: string; next?: true }
	| { kind: "llb"; id: string }
	| { kind: "fix"; hit: HitId; choice?: string }
	| { kind: "fixes"; fix?: string }
	| { kind: "attention" | "rules" | "key" | "llbs" };

export type Layer = "sprint" | "llb" | "cards" | "past" | "src";
export type Layers = Readonly<Record<Layer, boolean>>;

export const LAYER_ORDER: readonly Layer[] = ["sprint", "llb", "cards", "past", "src"];
export const DEFAULT_LAYERS: Layers = { sprint: false, llb: true, cards: true, past: false, src: false };

export type ReleasesSearch = {
	focus: TrainKey | null;
	me: boolean;
	/** null shows every app. */
	apps: ReadonlySet<AppId> | null;
	hotfixes: boolean;
	sheet: SheetEntry | null;
	/** Set by a link or a test; null means the viewer's stored layers. */
	layers: Layers | null;
	/** Board keys whose sprint row is hidden; null means the viewer's stored set. */
	hideBoards: ReadonlySet<string> | null;
	/** Sprint rows keep only the cards that touch the mobile apps. */
	mobile: boolean;
};

export type ReleasesSearchRaw = Partial<Record<keyof ReleasesSearch, string>>;

function str(v: unknown): string | null {
	if (typeof v === "string") return v;
	if (typeof v === "number" || typeof v === "boolean") return String(v);
	return null;
}

function flag(v: unknown): boolean | null {
	const s = str(v);
	if (s === "1" || s === "true") return true;
	if (s === "0" || s === "false") return false;
	return null;
}

function parseSheet(raw: string | null): SheetEntry | null {
	if (!raw) return null;
	const at = raw.indexOf(":");
	const head = at < 0 ? raw : raw.slice(0, at);
	const rest = at < 0 ? "" : raw.slice(at + 1);
	const dot = head.indexOf(".");
	const kind = dot < 0 ? head : head.slice(0, dot);
	if (kind === "fix") {
		const choice = dot < 0 ? undefined : head.slice(dot + 1);
		if (!rest || choice === "") return null;
		return choice ? { kind, hit: rest, choice } : { kind, hit: rest };
	}
	if (dot >= 0) return null;
	switch (kind) {
		case "attention":
		case "rules":
		case "key":
		case "llbs":
			return { kind };
		case "fixes":
			return rest ? { kind, fix: rest } : { kind };
		case "train":
			return rest ? { kind, train: rest } : null;
		case "card":
			return rest ? { kind, key: rest } : null;
		case "sprint": {
			const [board, next, extra] = rest.split(":");
			if (!board || extra !== undefined || (next !== undefined && next !== "next")) return null;
			return next ? { kind, board, next: true } : { kind, board };
		}
		case "llb":
			return rest ? { kind, id: rest } : null;
		case "mismatch":
			return rest ? { kind, hit: rest } : null;
		case "version": {
			const [train, app] = rest.split(":");
			return train && app ? { kind, train, app } : null;
		}
		case "hotfix": {
			const i = rest.indexOf(":");
			return i > 0 && i < rest.length - 1 ? { kind, app: rest.slice(0, i), version: rest.slice(i + 1) } : null;
		}
		default:
			return null;
	}
}

export function sheetParam(s: SheetEntry): string {
	switch (s.kind) {
		case "train":
			return `train:${s.train}`;
		case "version":
			return `version:${s.train}:${s.app}`;
		case "hotfix":
			return `hotfix:${s.app}:${s.version}`;
		case "mismatch":
			return `mismatch:${s.hit}`;
		case "card":
			return `card:${s.key}`;
		case "sprint":
			return s.next ? `sprint:${s.board}:next` : `sprint:${s.board}`;
		case "llb":
			return `llb:${s.id}`;
		case "fix":
			return s.choice ? `fix.${s.choice}:${s.hit}` : `fix:${s.hit}`;
		case "fixes":
			return s.fix ? `fixes:${s.fix}` : "fixes";
		default:
			return s.kind;
	}
}

function parseLayers(raw: string | null): Layers | null {
	if (raw === null) return null;
	const on = new Set(raw.split(","));
	return Object.fromEntries(LAYER_ORDER.map((k) => [k, on.has(k)])) as Record<Layer, boolean>;
}

export function parseBoardKeys(raw: string): ReadonlySet<string> {
	return new Set(raw.split(",").filter(Boolean));
}

export function boardKeysParam(keys: ReadonlySet<string>): string {
	return [...keys].sort().join(",");
}

export function parseReleasesSearch(raw: Record<string, unknown>): ReleasesSearch {
	const apps = str(raw.apps);
	const hideBoards = str(raw.hideBoards);
	return {
		focus: str(raw.focus) || null,
		me: flag(raw.me) ?? false,
		apps: apps ? new Set(apps.split(",").filter(Boolean)) : null,
		hotfixes: flag(raw.hotfixes) ?? true,
		sheet: parseSheet(str(raw.sheet)),
		layers: parseLayers(str(raw.layers)),
		hideBoards: hideBoards === null ? null : parseBoardKeys(hideBoards),
		mobile: flag(raw.mobile) ?? false,
	};
}

export function releasesSearch(s: ReleasesSearch): ReleasesSearchRaw {
	const out: ReleasesSearchRaw = {};
	if (s.focus) out.focus = s.focus;
	if (s.me) out.me = "1";
	if (s.apps) out.apps = [...s.apps].join(",");
	if (!s.hotfixes) out.hotfixes = "0";
	if (s.sheet) out.sheet = sheetParam(s.sheet);
	if (s.layers) out.layers = LAYER_ORDER.filter((k) => s.layers?.[k]).join(",");
	if (s.hideBoards) out.hideBoards = boardKeysParam(s.hideBoards);
	if (s.mobile) out.mobile = "1";
	return out;
}
