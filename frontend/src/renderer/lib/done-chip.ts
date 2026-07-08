import type { WorkspaceSession } from "../types/workspace";
import { formatTimeCompact } from "./format-time";

/**
 * How a session in the collapsed done bucket finished. The bucket only ever
 * holds `merged` or `terminated` sessions (see {@link attentionZone}), so the
 * split is binary: an explicitly terminated (killed/ended) session reads as
 * "terminated"; everything else (a merged session) reads as "done".
 */
export type DoneDisposition = "done" | "terminated";

export function doneDisposition(session: Pick<WorkspaceSession, "status">): DoneDisposition {
	return session.status === "terminated" ? "terminated" : "done";
}

/**
 * Relative-time caption for when a session moved into the done bucket, e.g.
 * "moved 2h ago" / "moved 3d ago" / "moved just now". The wire carries no
 * dedicated terminated/moved timestamp, so callers pass {@link WorkspaceSession.updatedAt}
 * — bumped by the daemon on the status change — as the best available signal.
 */
export function formatMovedAgo(iso: string | null | undefined): string {
	return `moved ${formatTimeCompact(iso)}`;
}

/**
 * Done-bucket sessions ordered most-recently-moved first (descending by
 * `updatedAt`). Returns a new array and never mutates the input; ties preserve
 * input order (Array.prototype.sort is stable), so an unparseable timestamp
 * (treated as epoch 0) sinks to the bottom without reordering its peers.
 */
export function sortDoneRecentFirst<T extends Pick<WorkspaceSession, "updatedAt">>(sessions: T[]): T[] {
	return [...sessions].sort((a, b) => movedTimestamp(b) - movedTimestamp(a));
}

function movedTimestamp(session: Pick<WorkspaceSession, "updatedAt">): number {
	const parsed = Date.parse(session.updatedAt);
	return Number.isNaN(parsed) ? 0 : parsed;
}
