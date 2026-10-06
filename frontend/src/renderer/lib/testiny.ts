import type { components } from "../../api/schema";
import type { WorkspaceSession } from "../types/workspace";

export type TestinyRun = components["schemas"]["TestinyRunView"];
export type TestinyCase = components["schemas"]["TestinyCaseResult"];
export type TestinyRunsResponse = components["schemas"]["TestinyRunsResponse"];
export type TestinyFetchErrorKind = components["schemas"]["TestinyFetchError"]["kind"];

/** The statuses Testiny records, in the order the summary chips show them. */
const SUMMARY_ORDER = ["PASSED", "FAILED", "BLOCKED", "SKIPPED", "NOTRUN"];

/**
 * How urgently a case that did not pass needs a look: a failure before a block
 * before one nobody played yet. A status Testiny adds later sorts with the
 * unplayed ones, so it stays visible instead of passing for done.
 */
const OPEN_RANK: Record<string, number> = { FAILED: 0, BLOCKED: 1, NOTRUN: 2, SKIPPED: 3 };
const UNKNOWN_RANK = 2;

export function orderCases(cases: TestinyCase[]): { open: TestinyCase[]; passed: TestinyCase[] } {
	const open = cases
		.filter((c) => c.status !== "PASSED")
		.map((c, index) => ({ c, index, rank: OPEN_RANK[c.status] ?? UNKNOWN_RANK }))
		.sort((a, b) => a.rank - b.rank || a.index - b.index)
		.map(({ c }) => c);
	return { open, passed: cases.filter((c) => c.status === "PASSED") };
}

export function summaryCounts(run: TestinyRun): { status: string; count: number }[] {
	const counts: Record<string, number> = { ...(run.counts ?? {}) };
	if (!run.counts) {
		for (const c of run.cases) counts[c.status] = (counts[c.status] ?? 0) + 1;
	}
	const extra = Object.keys(counts)
		.filter((s) => !SUMMARY_ORDER.includes(s))
		.sort();
	return [...SUMMARY_ORDER, ...extra]
		.map((status) => ({ status, count: counts[status] ?? 0 }))
		.filter((entry) => entry.count > 0);
}

/** How many of a run's cases a Maestro case script plays, or null when none does. */
export function scriptCoverage(cases: TestinyCase[]): { scripted: number; total: number } | null {
	const scripted = cases.filter((c) => c.script).length;
	return scripted === 0 ? null : { scripted, total: cases.length };
}

/**
 * The machine-wide reason every run failed to read, when there is one. A missing
 * CLI or a rejected key breaks every run the same way, so the tab says it once
 * above the cards instead of once per card.
 */
export function sharedBlocker(runs: TestinyRun[]): "auth" | "binary_missing" | null {
	const kind = runs[0]?.fetchError?.kind;
	if (kind !== "auth" && kind !== "binary_missing") return null;
	return runs.every((r) => r.fetchError?.kind === kind) ? kind : null;
}

const FETCH_REASON: Record<TestinyFetchErrorKind, string> = {
	auth: "Testiny rejected the key",
	binary_missing: "testiny CLI not found",
	unavailable: "Testiny did not answer",
	rejected: "Testiny refused the read",
	not_found: "Run not found in Testiny (deleted?)",
};

/**
 * The muted line a card shows when its latest read failed: how old the data on
 * screen is, or that there is none yet. The reason is left out when the banner
 * above the cards already gives it.
 */
export function runNotice(run: TestinyRun, now: number, banner: TestinyFetchErrorKind | null): string | null {
	const error = run.fetchError;
	if (!error) return null;
	if (error.kind === "not_found") return FETCH_REASON.not_found;
	const age = run.fetchedAt ? `showing data from ${ageLabel(run.fetchedAt, now)}` : "couldn't read this run yet";
	return banner === error.kind ? age : `${FETCH_REASON[error.kind]} · ${age}`;
}

export function ageLabel(iso: string, now: number): string {
	const minutes = Math.floor((now - Date.parse(iso)) / 60_000);
	if (!Number.isFinite(minutes) || minutes < 1) return "just now";
	if (minutes < 60) return `${minutes} min ago`;
	const hours = Math.floor(minutes / 60);
	if (hours < 24) return `${hours} h ago`;
	return `${Math.floor(hours / 24)} d ago`;
}

/**
 * Who linked a run, as a person reads it: "you" for the app, a crew member by its
 * role, else the session id the daemon recorded.
 */
export function linkedByLabel(linkedBy: string, sessions: Pick<WorkspaceSession, "id" | "crew">[]): string {
	if (!linkedBy) return "you";
	return sessions.find((s) => s.id === linkedBy)?.crew?.role ?? linkedBy;
}

const QA_EVIDENCE_ROOT = "/Desktop/QA Evidence/";

/**
 * A run's evidence folder, split so the rail can let the location shrink and
 * keep the run's own folder whole: `~/Desktop/QA Evidence/…/` + `TR-632 - iOS`.
 */
export function evidenceLabel(path: string): { location: string; folder: string } {
	const home = path.match(/^\/Users\/[^/]+/)?.[0] ?? "";
	const rest = (home ? "~" : "") + path.slice(home.length);
	const cut = rest.lastIndexOf("/") + 1;
	const folder = rest.slice(cut);
	const location = rest.startsWith(`~${QA_EVIDENCE_ROOT}`) ? `~${QA_EVIDENCE_ROOT}…/` : rest.slice(0, cut);
	return { location, folder };
}
