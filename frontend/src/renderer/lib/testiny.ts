import type { components } from "../../api/schema";
import { jiraKeyFromIssueId, type WorkspaceSession } from "../types/workspace";

export type TestinyRun = components["schemas"]["TestinyRunView"];
export type TestinyCase = components["schemas"]["TestinyCaseResult"];
export type TestinyRunsResponse = components["schemas"]["TestinyRunsResponse"];
export type TestinyFetchErrorKind = components["schemas"]["TestinyFetchError"]["kind"];
export type TestinyCaseDetail = components["schemas"]["DomainTestinyCaseDetail"];
type TestinyRecord = components["schemas"]["TestinyResultRecord"];

/** The statuses Testiny records, in the order the summary chips and the status menu show them. */
export const TESTINY_STATUSES = ["PASSED", "FAILED", "BLOCKED", "SKIPPED", "NOTRUN"] as const;
export type TestinyStatus = (typeof TESTINY_STATUSES)[number];

/** The longest comment Testiny takes on a result, as the daemon checks it. */
export const TESTINY_COMMENT_MAX = 300;

/**
 * One result a person sets from the tab. Testiny's rule, which the daemon
 * enforces too: a failed, blocked or skipped case says why; a passed or unplayed
 * one takes no comment.
 */
export type TestinyResultWrite =
	| { caseId: number; status: "PASSED" | "NOTRUN" }
	| { caseId: number; status: "FAILED" | "BLOCKED" | "SKIPPED"; comment: string };

export function needsComment(status: TestinyStatus): status is "FAILED" | "BLOCKED" | "SKIPPED" {
	return status === "FAILED" || status === "BLOCKED" || status === "SKIPPED";
}

/**
 * How urgently a case that did not pass needs a look: a failure before a block
 * before one nobody played yet. A status Testiny adds later sorts with the
 * unplayed ones, so it stays visible instead of passing for done.
 */
const OPEN_RANK: Record<string, number> = { FAILED: 0, BLOCKED: 1, NOTRUN: 2, SKIPPED: 3 };
const UNKNOWN_RANK = 2;

/** Where each case sits on a card, by id: the open list, then the passed fold. */
export type CaseOrder = { open: number[]; passed: number[] };

/**
 * Splits a run's cases into the open list and the passed fold. With `held`,
 * every case it names stays where it was whatever its status is now, so a row
 * the person just changed does not jump from under the pointer; a case it does
 * not name is placed by the usual rule after them.
 */
export function orderCases(cases: TestinyCase[], held?: CaseOrder): { open: TestinyCase[]; passed: TestinyCase[] } {
	const byId = new Map(cases.map((c) => [c.id, c]));
	const pick = (ids: number[]) => ids.flatMap((id) => byId.get(id) ?? []);
	const placed = new Set([...(held?.open ?? []), ...(held?.passed ?? [])]);
	const rest = cases.filter((c) => !placed.has(c.id));
	const open = rest
		.filter((c) => c.status !== "PASSED")
		.map((c, index) => ({ c, index, rank: OPEN_RANK[c.status] ?? UNKNOWN_RANK }))
		.sort((a, b) => a.rank - b.rank || a.index - b.index)
		.map(({ c }) => c);
	return {
		open: [...pick(held?.open ?? []), ...open],
		passed: [...pick(held?.passed ?? []), ...rest.filter((c) => c.status === "PASSED")],
	};
}

/**
 * The run with one case replaced by `next`, its counts moved across when
 * Testiny gave them (otherwise they are counted from the cases anyway).
 */
export function withCase(run: TestinyRun, next: TestinyCase): TestinyRun {
	const prev = run.cases.find((c) => c.id === next.id);
	if (!prev) return run;
	let counts = run.counts;
	if (counts && prev.status !== next.status) {
		counts = {
			...counts,
			[prev.status]: (counts[prev.status] ?? 1) - 1,
			[next.status]: (counts[next.status] ?? 0) + 1,
		};
		if (counts[prev.status] === 0) delete counts[prev.status];
	}
	return { ...run, counts, cases: run.cases.map((c) => (c.id === next.id ? next : c)) };
}

/** The run as it will read once the person's result is written: what the tab shows meanwhile. */
export function withResult(run: TestinyRun, write: TestinyResultWrite, at: string): TestinyRun {
	const prev = run.cases.find((c) => c.id === write.caseId);
	if (!prev) return run;
	const comment = "comment" in write ? write.comment : "";
	return withCase(run, {
		...prev,
		status: write.status,
		recorded: { status: write.status, comment, by: "", sha: "", at },
	});
}

export function summaryCounts(run: TestinyRun): { status: string; count: number }[] {
	const counts: Record<string, number> = { ...(run.counts ?? {}) };
	if (!run.counts) {
		for (const c of run.cases) counts[c.status] = (counts[c.status] ?? 0) + 1;
	}
	const extra = Object.keys(counts)
		.filter((s) => !(TESTINY_STATUSES as readonly string[]).includes(s))
		.sort();
	return [...TESTINY_STATUSES, ...extra]
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
	cli_too_old: "testiny CLI needs an update",
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

/**
 * The result AO wrote for a case, while Testiny still has that status. Undefined
 * when AO wrote none, or when someone changed the case in Testiny since, so the
 * tab never vouches for a status, or gives a reason for one, that AO did not set.
 */
export function standingRecord(testCase: TestinyCase): TestinyRecord | undefined {
	const r = testCase.recorded;
	return r && r.status === testCase.status ? r : undefined;
}

/** Who set a case's standing result through AO, when, and on which commit: "set by qa · 5 min ago · on 4f2c9e1". */
export function provenanceLabel(
	testCase: TestinyCase,
	now: number,
	sessions: Pick<WorkspaceSession, "id" | "crew">[],
): string | null {
	const r = standingRecord(testCase);
	if (!r) return null;
	const who = r.byRole || linkedByLabel(r.by, sessions);
	const parts = [`set by ${who}`, ageLabel(r.at, now)];
	if (r.sha) parts.push(`on ${r.sha.slice(0, 7)}`);
	return parts.join(" · ");
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

/** One fact about a case, with the field it came from, so a bare "High" or "iOS" still says what it is. */
export type CaseChip = { field: string; value: string };

/** What a case is about, in the order the detail panel's meta line reads it. Unset fields are left out. */
export function caseChips(detail: TestinyCaseDetail): CaseChip[] {
	const chips: CaseChip[] = [];
	const add = (field: string, value: string | undefined) => {
		if (value) chips.push({ field, value });
	};
	add("Priority", detail.priority?.label);
	add("Type", sentenceCase(detail.type));
	for (const platform of detail.platforms) add("Platform", platform);
	add("Jira", detail.jira);
	for (const status of detail.automation) add("Automation", status);
	return chips;
}

/**
 * Where the case sits in the product, as label/value rows: these run long
 * ("Share > Empty state when the account has never shared a fund"), which a
 * chip can only wrap into a block.
 */
export function caseFacts(detail: TestinyCaseDetail): [string, string][] {
	const facts: [string, string][] = [];
	const feature = [detail.features, detail.subFeatures].filter(Boolean).join(" > ");
	if (feature) facts.push(["Feature", feature]);
	if (detail.section) facts.push(["Section", detail.section]);
	return facts;
}

/**
 * The task's Jira key while the case is not linked to it as a requirement, so
 * the panel can say what qa still has to link; null once it is, or when the
 * task (`issueId`, e.g. "jira:MOBILITY-4839") has no Jira issue.
 */
export function unlinkedTaskIssue(detail: TestinyCaseDetail, issueId: string | undefined): string | null {
	const key = jiraKeyFromIssueId(issueId);
	if (!key) return null;
	return detail.requirements.some((r) => r.key.toUpperCase() === key.toUpperCase()) ? null : key;
}

/** Testiny's FUNCTIONAL or NON_FUNCTIONAL, as a person writes it. */
function sentenceCase(value: string): string {
	const words = value.replace(/_/g, " ").toLowerCase();
	return words.charAt(0).toUpperCase() + words.slice(1);
}

/** A run of a case's rich text: plain lines, or a flat list. */
export type TextBlock =
	{ kind: "text"; text: string } | { kind: "list"; ordered: boolean; start: number; items: string[] };

/** "- item" or "3. item": the number when there is one, then the item. */
const LIST_LINE = /^(?:(\d+)\.|-) (.*)$/;

/**
 * Splits the testiny CLI's plain-text rendering of a rich-text field into blocks, so
 * "- " and "1. " runs show as real lists. Text with an indented line (a nested
 * list, or an item that wraps onto its own lines) is kept whole, as written: a
 * flat list would lose its shape. A blank line ends a block.
 */
export function textBlocks(text: string): TextBlock[] {
	if (text.trim() === "") return [];
	const lines = text.split("\n");
	if (lines.some((line) => /^\s+\S/.test(line))) return [{ kind: "text", text }];
	const blocks: TextBlock[] = [];
	let open: TextBlock | null = null;
	for (const line of lines) {
		if (line.trim() === "") {
			open = null;
			continue;
		}
		const listed = LIST_LINE.exec(line);
		if (listed) {
			const [, number, item] = listed;
			const ordered = number !== undefined;
			if (open?.kind === "list" && open.ordered === ordered) {
				open.items.push(item);
				continue;
			}
			open = { kind: "list", ordered, start: ordered ? Number(number) : 1, items: [item] };
		} else if (open?.kind === "text") {
			open.text += `\n${line}`;
			continue;
		} else {
			open = { kind: "text", text: line };
		}
		blocks.push(open);
	}
	return blocks;
}
