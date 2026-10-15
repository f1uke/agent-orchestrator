import { create } from "zustand";
import { DEFAULT_LAYERS, type Layer, type Layers, type SheetEntry } from "../lib/releases/url";

const LAYERS_KEY = "ao.releases.layers.v1";
const FOLDS_KEY = "ao.releases.folds.v1";

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
	showLayer: (k: Layer, folds: readonly string[]) => void;
	pushBack: (s: SheetEntry) => void;
	popBack: () => SheetEntry | undefined;
	clearBack: () => void;
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
	showLayer: (k, folds) => {
		get().setLayer(k, true);
		get().openFolds(folds);
	},
	pushBack: (s) => set({ back: [...get().back, s] }),
	popBack: () => {
		const back = get().back;
		set({ back: back.slice(0, -1) });
		return back.at(-1);
	},
	clearBack: () => set({ back: [] }),
}));
