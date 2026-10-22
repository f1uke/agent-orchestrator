import type { AlertHit, AppId, Board, TrainKey } from "./types";

function hitCardKeys(h: AlertHit): readonly string[] {
	const s = h.subject;
	switch (s.kind) {
		case "card":
		case "cardOnTrain":
			return [s.key];
		case "pageFamily":
			return s.keys;
		case "mr":
			return typeof h.evidence.card === "string" ? [h.evidence.card] : [];
		default:
			return [];
	}
}

export function cardHits(hits: readonly AlertHit[], key: string): AlertHit[] {
	return hits.filter((h) => hitCardKeys(h).includes(key));
}

export function versionHits(hits: readonly AlertHit[], b: Board, train: TrainKey, app: AppId): AlertHit[] {
	const family = b.apps.find((a) => a.id === app)?.family;
	return hits.filter((h) => {
		if (h.train !== train) return false;
		if (h.app) return h.app === app;
		return h.subject.kind !== "pageFamily" || h.subject.family === family;
	});
}

type TextPart = { text: string } | { key: string };

export function splitKeys(text: string, keys: readonly string[]): TextPart[] {
	if (!keys.length) return [{ text }];
	const known = new Set(keys);
	return text
		.split(new RegExp(`\\b(${keys.join("|")})\\b`))
		.filter(Boolean)
		.map((p) => (known.has(p) ? { key: p } : { text: p }));
}
