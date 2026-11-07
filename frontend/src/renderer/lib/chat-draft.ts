/**
 * Unsent composer text, kept per session in the renderer's localStorage (which Electron keeps under
 * ~/.ao/electron) so it survives switching pages or sessions and an app restart. Only the text and the
 * shell/prompt mode are kept: attachments hold File objects and image data that do not belong in storage.
 */

export type DraftMode = "prompt" | "bash";
export type Draft = { text: string; mode: DraftMode };

type Entry = Draft & { at: number };

const STORAGE_KEY = "ao.chat.drafts";
/** Drafts of sessions that are gone are never asked for again; keep only the most recently edited ones. */
export const MAX_DRAFTS = 50;

function storage(): Storage | null {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

function readAll(): Record<string, Entry> {
	try {
		const parsed: unknown = JSON.parse(storage()?.getItem(STORAGE_KEY) ?? "{}");
		if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
		const out: Record<string, Entry> = {};
		for (const [id, value] of Object.entries(parsed as Record<string, unknown>)) {
			const v = value as Partial<Entry> | null;
			if (!v || typeof v.text !== "string" || v.text === "") continue;
			out[id] = { text: v.text, mode: v.mode === "bash" ? "bash" : "prompt", at: typeof v.at === "number" ? v.at : 0 };
		}
		return out;
	} catch {
		return {};
	}
}

function writeAll(entries: Record<string, Entry>) {
	const kept = Object.entries(entries)
		.sort(([, a], [, b]) => b.at - a.at)
		.slice(0, MAX_DRAFTS);
	try {
		const s = storage();
		if (!s) return;
		if (kept.length === 0) s.removeItem(STORAGE_KEY);
		else s.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries(kept)));
	} catch {
		// Storage full or unavailable: the draft stays in memory for this mount only.
	}
}

export function loadDraft(sessionId: string): Draft | null {
	const entry = readAll()[sessionId];
	return entry ? { text: entry.text, mode: entry.mode } : null;
}

/** Stores the draft, or forgets it when the text is empty. */
export function saveDraft(sessionId: string, draft: Draft, now = Date.now()) {
	const entries = readAll();
	if (draft.text === "") {
		if (!(sessionId in entries)) return;
		delete entries[sessionId];
	} else {
		entries[sessionId] = { ...draft, at: now };
	}
	writeAll(entries);
}
