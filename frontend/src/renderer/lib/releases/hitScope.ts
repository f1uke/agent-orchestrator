import { onlyMeSubject } from "./mineWhy";
import { cardSummary } from "./subject";
import type { AlertHit, AppId, Board, LLBGroup, TrainKey } from "./types";

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

export function llbHits(hits: readonly AlertHit[], g: LLBGroup): AlertHit[] {
	return hits.filter((h) => h.evidence.group === g.id);
}

export function versionHits(hits: readonly AlertHit[], b: Board, train: TrainKey, app: AppId): AlertHit[] {
	const family = b.apps.find((a) => a.id === app)?.family;
	return hits.filter((h) => {
		if (h.train !== train) return false;
		if (h.app) return h.app === app;
		return h.subject.kind !== "pageFamily" || h.subject.family === family;
	});
}

export type CardLine = { keys: string[]; more: number } | { summary: string } | null;

const MAX_KEYS = 5;

const mentions = (text: string, key: string) => new RegExp(`\\b${key}\\b`).test(text);

export function cardLine(h: AlertHit, b: Board, me: boolean, what: string): CardLine {
	const subject = onlyMeSubject(h, me);
	const allowed = subject.kind === "pageFamily" ? new Set(subject.keys) : null;
	const keys = allowed ? h.cards.filter((k) => allowed.has(k)) : h.cards;
	if (keys.length === 1 && mentions(what, keys[0])) {
		const summary = cardSummary(b, keys[0]);
		return summary ? { summary } : null;
	}
	const rest = keys.filter((k) => !mentions(what, k));
	return rest.length ? { keys: rest.slice(0, MAX_KEYS), more: Math.max(0, rest.length - MAX_KEYS) } : null;
}
