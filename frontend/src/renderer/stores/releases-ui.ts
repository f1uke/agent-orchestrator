import { create } from "zustand";
import {
	DEFAULT_LAYERS,
	LAYER_ORDER,
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
const HIDDEN_KEY = "ao.releases.hidden-boards.v1";

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

function readKeys(storageKey: string): ReadonlySet<string> {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(storageKey) ?? "[]");
		return new Set(Array.isArray(saved) ? saved.filter((k): k is string => typeof k === "string" && k !== "") : []);
	} catch {
		return new Set();
	}
}

function writeKeys(storageKey: string, keys: ReadonlySet<string>): void {
	try {
		window.localStorage.setItem(storageKey, JSON.stringify([...keys]));
	} catch {}
}

function readLayers(): Layers {
	try {
		const raw = window.localStorage.getItem(LAYERS_KEY);
		if (!raw) return DEFAULT_LAYERS;
		const saved = JSON.parse(raw) as Partial<Record<Layer, unknown>>;
		const pick = (k: Layer) => (typeof saved[k] === "boolean" ? (saved[k] as boolean) : DEFAULT_LAYERS[k]);
		return Object.fromEntries(LAYER_ORDER.map((k) => [k, pick(k)])) as Record<Layer, boolean>;
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
	setLayers: (layers: Layers) => void;
	toggleFold: (key: string) => void;
	openFolds: (keys: readonly string[]) => void;
	pushBack: (s: SheetEntry) => void;
	popBack: () => SheetEntry | undefined;
	clearBack: () => void;
	scroll: Scroll | null;
	setScroll: (s: Scroll) => void;
	/** Kept for a board with no active sprint too, so its row stays hidden when the next sprint starts. */
	hidden: ReadonlySet<string>;
	setHidden: (keys: ReadonlySet<string>) => void;
};

export const useReleasesUi = create<ReleasesUi>((set, get) => ({
	layers: readLayers(),
	open: readKeys(FOLDS_KEY),
	back: [],
	setLayers: (layers) => {
		writeLayers(layers);
		set({ layers });
	},
	toggleFold: (key) => {
		const open = new Set(get().open);
		if (open.has(key)) open.delete(key);
		else open.add(key);
		writeKeys(FOLDS_KEY, open);
		set({ open });
	},
	openFolds: (keys) => {
		const open = new Set([...get().open, ...keys]);
		writeKeys(FOLDS_KEY, open);
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
	hidden: readKeys(HIDDEN_KEY),
	setHidden: (hidden) => {
		writeKeys(HIDDEN_KEY, hidden);
		set({ hidden });
	},
}));
