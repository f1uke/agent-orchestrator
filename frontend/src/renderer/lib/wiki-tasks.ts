/**
 * The Tasks tab's own reading of the rows the daemon sent: which ones the
 * reader asked to see, and how they fall into days.
 *
 * All of it is pure. The daemon returns EVERY row under the configured
 * subtree, annotated; the cutoff and the owner filter are applied here so the
 * tab can say "N rows are hidden — show them" and flip it with no round trip,
 * and so a filtered list can never be mistaken for a destroyed backlog.
 */

import type { WikiTaskRow } from "../hooks/useWiki";

/**
 * Which rows the reader wants.
 *
 * `mine` counts an UNOWNED row as the reader's own: a row that names nobody in
 * your own notes is yours by default, and the alternative — hiding every row
 * that forgot a token — would make the filter lose work rather than focus it.
 */
export type OwnerFilter = "all" | "mine" | "others";

/** A day's worth of rows, as the tab draws them. */
export type TaskGroup = {
	/** `overdue`, `undated`, or the day itself as YYYY-MM-DD. */
	key: string;
	label: string;
	rows: WikiTaskRow[];
};

/** What `partitionTasks` decided, including what it hid and why. */
export type TaskView = {
	groups: TaskGroup[];
	/** Rows left after every filter — the number the tab counts. */
	visible: number;
	/** Rows the cutoff hid. They are still in the notes, untouched. */
	hiddenByCutoff: number;
	/** Rows the owner filter hid. */
	hiddenByOwner: number;
};

/** Today as YYYY-MM-DD in the reader's own timezone, not UTC. */
export function today(now = new Date()): string {
	const pad = (n: number) => String(n).padStart(2, "0");
	return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
}

/**
 * Whether a row belongs to the reader.
 *
 * Matching is case-insensitive and ignores surrounding whitespace, because
 * "@Fluke" and "@fluke" are the same person to whoever typed them. An alias
 * may be written with or without its "@".
 */
export function isMine(row: WikiTaskRow, aliases: string[]): boolean {
	const owner = (row.owner ?? "").trim().toLowerCase();
	if (owner === "") return true;
	return aliases.some((alias) => {
		const want = alias.trim().replace(/^@/, "").toLowerCase();
		return want !== "" && want === owner;
	});
}

/**
 * The date a row is judged by.
 *
 * `due:` when the row carries one; otherwise the NOTE's mtime. A row has no
 * date of its own, so the note's is the only other honest answer — and the
 * cutoff means "start counting from here", which is a question about how stale
 * the work is, not about when it is due.
 *
 * 🗝 This fallback is used for the CUTOFF only, never for grouping. Grouping by
 * note mtime would drop every undated row in a note into the day that note was
 * last touched, which is a claim the note never made.
 */
export function effectiveDate(row: WikiTaskRow): string | null {
	if (row.due) return row.due;
	if (!row.noteModifiedAt) return null;
	const at = new Date(row.noteModifiedAt);
	return Number.isNaN(at.getTime()) ? null : today(at);
}

/**
 * Split the rows into the day groups the tab draws, reporting what was hidden.
 *
 * Order: overdue first (it is the only group that is late), then today, then
 * each future day, then the undated rows last. Undated is last rather than
 * first because a dated row is a commitment and an undated one is a list — but
 * it is never hidden, and with no `due:` anywhere in a vault it is simply the
 * only group there is.
 */
export function partitionTasks(
	rows: WikiTaskRow[],
	options: {
		ownerFilter: OwnerFilter;
		ownerAliases: string[];
		/** YYYY-MM-DD. Rows dated before it are hidden. Empty means no cutoff. */
		cutoff?: string;
		/** When true the cutoff is reported but not applied. */
		showHidden?: boolean;
		now?: Date;
	},
): TaskView {
	const { ownerFilter, ownerAliases, cutoff, showHidden = false, now = new Date() } = options;
	const stamp = today(now);
	let hiddenByCutoff = 0;
	let hiddenByOwner = 0;
	const kept: WikiTaskRow[] = [];

	for (const row of rows) {
		const mine = isMine(row, ownerAliases);
		if ((ownerFilter === "mine" && !mine) || (ownerFilter === "others" && mine)) {
			hiddenByOwner += 1;
			continue;
		}
		// A row with no date at all is never hidden by the cutoff. "We do not
		// know how old this is" is not the claim "it is older than the cutoff",
		// and hiding it would lose work with nothing to show for it.
		const at = cutoff ? effectiveDate(row) : null;
		if (cutoff && at !== null && at < cutoff) {
			hiddenByCutoff += 1;
			if (!showHidden) continue;
		}
		kept.push(row);
	}

	const byKey = new Map<string, WikiTaskRow[]>();
	for (const row of kept) {
		const key = !row.due ? "undated" : row.due < stamp ? "overdue" : row.due;
		const bucket = byKey.get(key);
		if (bucket) bucket.push(row);
		else byKey.set(key, [row]);
	}

	const groups: TaskGroup[] = [];
	const push = (key: string) => {
		const rowsFor = byKey.get(key);
		if (rowsFor) groups.push({ key, label: groupLabel(key, stamp), rows: rowsFor });
	};
	push("overdue");
	push(stamp);
	for (const key of [...byKey.keys()].filter((k) => k !== "overdue" && k !== "undated" && k !== stamp).sort()) {
		push(key);
	}
	push("undated");

	return { groups, visible: kept.length, hiddenByCutoff, hiddenByOwner };
}

/**
 * A group's heading. Dated groups say the day AND the date, because "Thursday"
 * alone is ambiguous past this week and a bare date makes the reader count.
 */
export function groupLabel(key: string, stamp: string): string {
	if (key === "overdue") return "Overdue";
	if (key === "undated") return "No due date";
	if (key === stamp) return "Today";
	const at = new Date(`${key}T00:00:00`);
	if (Number.isNaN(at.getTime())) return key;
	const weekday = at.toLocaleDateString(undefined, { weekday: "long" });
	const date = at.toLocaleDateString(undefined, { day: "numeric", month: "short" });
	return `${weekday} · ${date}`;
}

/**
 * Rows in the order the tab lists them within a day: by note, then by position
 * in it, so a note's rows stay in the order they were written rather than
 * being scattered by an alphabetical sort of their text.
 *
 * The daemon already returns them this way; this exists so a caller that
 * re-orders (a filter, a concat) can put them back.
 */
export function byNoteThenLine(a: WikiTaskRow, b: WikiTaskRow): number {
	if (a.path !== b.path) return a.path.localeCompare(b.path);
	return a.line - b.line;
}
