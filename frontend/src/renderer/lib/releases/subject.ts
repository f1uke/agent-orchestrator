import { trainLabel } from "./llb";
import type { AppId, Board, HitId, Subject } from "./types";
import type { SheetEntry } from "./url";

export type SubjectLabel = { app: AppId | null; text: string };

const DATE_TEXT = { freeze: "freeze date", publish: "publish date", page: "train page" } as const;

function versionText(b: Board, train: string, app: AppId): string {
	const v = b.trains.find((t) => t.id === train)?.versions.find((x) => x.app === app);
	return v ? `${v.version} · ${trainLabel(b, train)}` : trainLabel(b, train);
}

function cardSummary(b: Board, key: string): string | undefined {
	for (const row of b.sprints) for (const c of row.cards) if (c.key === key) return c.summary;
	for (const t of b.trains) for (const v of t.versions) for (const u of v.cards) if (u.key === key) return u.summary;
	for (const g of b.llbs) for (const br of g.branches) for (const c of br.cards) if (c.key === key) return c.summary;
	return undefined;
}

function cardText(b: Board, key: string): string {
	const summary = cardSummary(b, key);
	return summary ? `${key} ${summary}` : key;
}

export function subjectLabel(s: Subject, b: Board): SubjectLabel {
	switch (s.kind) {
		case "version":
			return { app: s.app, text: versionText(b, s.train, s.app) };
		case "versionDate":
			return { app: s.app, text: `${versionText(b, s.train, s.app)} · ${DATE_TEXT[s.field]}` };
		case "jiraVersion":
			return { app: s.app, text: `Jira release ${s.name}` };
		case "milestone":
			return { app: s.app, text: `milestone ${s.title}` };
		case "hotfix":
			return { app: s.app, text: `hotfix ${s.version}` };
		case "hotfixTrain":
			return { app: s.app, text: `hotfix ${s.version} in ${trainLabel(b, s.train)}` };
		case "trainPair":
			return { app: null, text: `${trainLabel(b, s.a)} and ${trainLabel(b, s.b)}` };
		case "cardOnTrain":
			return { app: s.app, text: `${cardText(b, s.key)} · ${trainLabel(b, s.train)}` };
		case "card":
			return { app: null, text: cardText(b, s.key) };
		case "mr":
			return { app: s.app, text: `!${s.iid} ${s.source} → ${s.target}` };
		case "pageFamily": {
			const cards = s.bucket === "merged" ? "merged cards" : "cards with an open MR";
			return { app: null, text: `${trainLabel(b, s.train)} ${s.family} page · ${s.keys.length} ${cards} missing` };
		}
	}
}

/** The sheet that tells the subject's whole story, for the alert's detail link. */
export function subjectSheet(s: Subject, hit: HitId): SheetEntry | null {
	switch (s.kind) {
		case "version":
		case "versionDate":
			return { kind: "version", train: s.train, app: s.app };
		case "cardOnTrain":
		case "card":
			return { kind: "card", key: s.key };
		case "hotfix":
		case "hotfixTrain":
			return { kind: "hotfix", app: s.app, version: s.version };
		case "trainPair":
			return { kind: "train", train: s.b };
		case "pageFamily":
			return { kind: "train", train: s.train };
		case "jiraVersion":
		case "milestone":
			return { kind: "mismatch", hit };
		case "mr":
			return null;
	}
}
