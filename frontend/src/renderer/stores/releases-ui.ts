import { create } from "zustand";
import {
	DEFAULT_LAYERS,
	parseReleasesSearch,
	releasesSearch,
	type Layer,
	type Layers,
	type ReleasesSearchRaw,
	type SheetEntry,
} from "../lib/releases/url";

const LAYERS_KEY = "ao.releases.layers.v1";
const FOLDS_KEY = "ao.releases.folds.v1";
const SEARCH_KEY = "ao.releases.search.v1";
const SCROLL_KEY = "ao.releases.scroll.v1";

export function readSavedSearch(): ReleasesSearchRaw {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(SEARCH_KEY) ?? "{}");
		return saved && typeof saved === "object"
			? releasesSearch(parseReleasesSearch(saved as Record<string, unknown>))
			: {};
	} catch {
		return {};
	}
}

export function saveSearch(search: ReleasesSearchRaw): void {
	try {
		window.localStorage.setItem(SEARCH_KEY, JSON.stringify(search));
	} catch {}
}

export type Scroll = { left: number; top: number; focus: string | null };

function readScroll(): Scroll | null {
	try {
		const s = JSON.parse(window.localStorage.getItem(SCROLL_KEY) ?? "null") as Partial<Scroll> | null;
		return s && typeof s.left === "number" && typeof s.top === "number"
			? { left: s.left, top: s.top, focus: typeof s.focus === "string" ? s.focus : null }
			: null;
	} catch {
		return null;
	}
}

function readFolds(): ReadonlySet<string> {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(FOLDS_KEY) ?? "[]");
		return new Set(Array.isArray(saved) ? saved.filter((k): k is string => typeof k === "string") : []);
	} catch {
		return new Set();
	}
}

function writeFolds(open: ReadonlySet<string>): void {
	try {
		window.localStorage.setItem(FOLDS_KEY, JSON.stringify([...open]));
	} catch {}
}

function readLayers(): Layers {
	try {
		const raw = window.localStorage.getItem(LAYERS_KEY);
		if (!raw) return DEFAULT_LAYERS;
		const saved = JSON.parse(raw) as Partial<Record<Layer, unknown>>;
		const pick = (k: Layer) => (typeof saved[k] === "boolean" ? (saved[k] as boolean) : DEFAULT_LAYERS[k]);
		return { sprint: pick("sprint"), llb: pick("llb"), cards: pick("cards"), past: pick("past") };
	} catch {
		return DEFAULT_LAYERS;
	}
}

function writeLayers(l: Layers): void {
	try {
		window.localStorage.setItem(LAYERS_KEY, JSON.stringify(l));
	} catch {}
}

type ReleasesUi = {
	layers: Layers;
	open: ReadonlySet<string>;
	/** Sheets under the one in the URL, for the back button. */
	back: SheetEntry[];
	setLayer: (k: Layer, on: boolean) => void;
	toggleFold: (key: string) => void;
	openFolds: (keys: readonly string[]) => void;
	pushBack: (s: SheetEntry) => void;
	popBack: () => SheetEntry | undefined;
	clearBack: () => void;
	scroll: Scroll | null;
	setScroll: (s: Scroll) => void;
};

export const useReleasesUi = create<ReleasesUi>((set, get) => ({
	layers: readLayers(),
	open: readFolds(),
	back: [],
	setLayer: (k, on) => {
		const layers = { ...get().layers, [k]: on };
		writeLayers(layers);
		set({ layers });
	},
	toggleFold: (key) => {
		const open = new Set(get().open);
		if (open.has(key)) open.delete(key);
		else open.add(key);
		writeFolds(open);
		set({ open });
	},
	openFolds: (keys) => {
		const open = new Set([...get().open, ...keys]);
		writeFolds(open);
		set({ open });
	},
	pushBack: (s) => set({ back: [...get().back, s] }),
	popBack: () => {
		const back = get().back;
		set({ back: back.slice(0, -1) });
		return back.at(-1);
	},
	clearBack: () => set({ back: [] }),
	scroll: readScroll(),
	setScroll: (scroll) => {
		try {
			window.localStorage.setItem(SCROLL_KEY, JSON.stringify(scroll));
		} catch {}
		set({ scroll });
	},
}));
