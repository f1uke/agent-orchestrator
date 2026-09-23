import { jiraKeyFromIssueId, type WorkspaceSession } from "../types/workspace";

/**
 * How a session in the board's Done lane finished. The lane only ever holds
 * `merged` or `terminated` sessions (see {@link attentionZone}), so the split is
 * binary: an explicitly terminated (killed/ended) session reads as
 * "terminated"; everything else (a merged session) reads as "done".
 */
export type DoneDisposition = "done" | "terminated";

export function doneDisposition(session: Pick<WorkspaceSession, "status">): DoneDisposition {
	return session.status === "terminated" ? "terminated" : "done";
}

type DoneTimed = Pick<WorkspaceSession, "updatedAt" | "termination">;

/**
 * When a session ended: the daemon's own account of the ending
 * (`termination.at`) when it kept one. A session that ended before AO recorded
 * endings has none, and falls back to `updatedAt`, which the daemon bumps on
 * the status change that put it here - close, but also moved by anything that
 * touched the row afterwards.
 */
export function doneAt(session: DoneTimed): string {
	return session.termination?.at || session.updatedAt;
}

/**
 * Done-lane sessions ordered most-recently-ended first (descending by
 * {@link doneAt}). Returns a new array and never mutates the input; ties
 * preserve input order (Array.prototype.sort is stable), so an unparseable
 * timestamp (treated as epoch 0) sinks to the bottom without reordering its
 * peers.
 */
export function sortDoneRecentFirst<T extends DoneTimed>(sessions: T[]): T[] {
	return sessions
		.map((session) => ({ session, at: endedTimestamp(session) }))
		.sort((a, b) => b.at - a.at)
		.map(({ session }) => session);
}

function endedTimestamp(session: DoneTimed): number {
	const parsed = Date.parse(doneAt(session));
	return Number.isNaN(parsed) ? 0 : parsed;
}

/**
 * Everything the Done lane's search box matches a session on, lower-cased once:
 * its display name, its id, its branch, its tracker issue (so a bare Jira key
 * finds it) and its PR/MR numbers in both spellings (`#318`, `!318`), since a
 * finished session is as often remembered by the change it shipped.
 */
export function doneSearchText(
	session: Pick<WorkspaceSession, "title" | "id" | "branch" | "issueId"> & { prs: readonly { number: number }[] },
): string {
	const parts = [session.title, session.id, session.branch, session.issueId, jiraKeyFromIssueId(session.issueId)];
	for (const pr of session.prs) parts.push(`#${pr.number}`, `!${pr.number}`);
	return parts.filter(Boolean).join("\n").toLowerCase();
}

/**
 * Whether a session's {@link doneSearchText} answers a query. Every
 * whitespace-separated term must appear somewhere, in any order, so
 * "retry notif" finds "Kill runaway notification retry loop". A blank query
 * matches everything.
 */
export function matchesDoneQuery(searchText: string, query: string): boolean {
	const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
	return terms.every((term) => searchText.includes(term));
}
