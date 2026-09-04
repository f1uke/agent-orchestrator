import { describe, expect, it } from "vitest";
import type { WikiTaskRow } from "../hooks/useWiki";
import { effectiveDate, isMine, partitionTasks, today } from "./wiki-tasks";

const NOW = new Date("2026-09-04T12:00:00");

function row(over: Partial<WikiTaskRow> = {}): WikiTaskRow {
	return {
		id: over.id ?? Math.random().toString(36).slice(2),
		path: "Areas/a.md",
		line: 1,
		raw: "- [ ] a row",
		text: "a row",
		...over,
	};
}

describe("isMine", () => {
	it("counts an unowned row as the reader's own", () => {
		// A row that names nobody in your OWN notes is yours by default. The
		// alternative would make "Mine" lose work rather than focus it.
		expect(isMine(row(), [])).toBe(true);
		expect(isMine(row({ owner: "" }), ["me"])).toBe(true);
	});

	it("matches an alias case-insensitively, with or without the @", () => {
		expect(isMine(row({ owner: "Fluke" }), ["fluke"])).toBe(true);
		expect(isMine(row({ owner: "fluke" }), ["@Fluke"])).toBe(true);
		expect(isMine(row({ owner: "Fluke" }), ["  Fluke  "])).toBe(true);
	});

	it("does not claim somebody else's row", () => {
		expect(isMine(row({ owner: "Someone Else" }), ["fluke"])).toBe(false);
	});

	it("never matches on an empty alias", () => {
		expect(isMine(row({ owner: "Someone" }), ["", "  ", "@"])).toBe(false);
	});
});

describe("effectiveDate", () => {
	it("prefers the row's own due date", () => {
		expect(effectiveDate(row({ due: "2026-01-02", noteModifiedAt: "2026-08-01T00:00:00Z" }))).toBe("2026-01-02");
	});

	it("falls back to the note's mtime, in the reader's timezone", () => {
		const at = new Date("2026-08-01T10:00:00Z");
		expect(effectiveDate(row({ noteModifiedAt: at.toISOString() }))).toBe(today(at));
	});

	it("is null when there is nothing to go on", () => {
		expect(effectiveDate(row())).toBeNull();
	});
});

describe("partitionTasks grouping", () => {
	it("orders overdue, today, future days, then undated", () => {
		const view = partitionTasks(
			[
				row({ id: "u", due: undefined }),
				row({ id: "later", due: "2026-09-20" }),
				row({ id: "soon", due: "2026-09-05" }),
				row({ id: "today", due: "2026-09-04" }),
				row({ id: "late", due: "2026-08-01" }),
			],
			{ ownerFilter: "all", ownerAliases: [], now: NOW },
		);
		expect(view.groups.map((g) => g.key)).toEqual(["overdue", "2026-09-04", "2026-09-05", "2026-09-20", "undated"]);
		expect(view.groups[0].label).toBe("Overdue");
		expect(view.groups[1].label).toBe("Today");
		expect(view.groups[4].label).toBe("No due date");
	});

	/**
	 * The rule the plan committed to: an undated row does NOT inherit the
	 * note's mtime for GROUPING. One edit to a 40-row note would otherwise drop
	 * all 40 into "today", which is a claim the note never made.
	 */
	it("never dates a row by its note's mtime", () => {
		const view = partitionTasks([row({ noteModifiedAt: "2026-09-04T09:00:00Z" })], {
			ownerFilter: "all",
			ownerAliases: [],
			now: NOW,
		});
		expect(view.groups.map((g) => g.key)).toEqual(["undated"]);
	});

	it("groups every row when a vault uses no due dates at all", () => {
		const view = partitionTasks([row({ id: "a" }), row({ id: "b" }), row({ id: "c" })], {
			ownerFilter: "all",
			ownerAliases: [],
			now: NOW,
		});
		expect(view.groups).toHaveLength(1);
		expect(view.groups[0].rows).toHaveLength(3);
		expect(view.visible).toBe(3);
	});
});

describe("partitionTasks owner filter", () => {
	const rows = [
		row({ id: "mine-unowned" }),
		row({ id: "mine-named", owner: "Fluke" }),
		row({ id: "theirs", owner: "Someone" }),
	];

	it("mine keeps the reader's rows and the unowned ones", () => {
		const view = partitionTasks(rows, { ownerFilter: "mine", ownerAliases: ["Fluke"], now: NOW });
		expect(view.visible).toBe(2);
		expect(view.hiddenByOwner).toBe(1);
	});

	it("others is the exact complement", () => {
		const view = partitionTasks(rows, { ownerFilter: "others", ownerAliases: ["Fluke"], now: NOW });
		expect(view.visible).toBe(1);
		expect(view.groups[0].rows[0].id).toBe("theirs");
	});

	it("all hides nothing", () => {
		const view = partitionTasks(rows, { ownerFilter: "all", ownerAliases: ["Fluke"], now: NOW });
		expect(view.visible).toBe(3);
		expect(view.hiddenByOwner).toBe(0);
	});
});

describe("partitionTasks cutoff", () => {
	const rows = [
		row({ id: "old", due: "2026-01-01" }),
		row({ id: "new", due: "2026-09-10" }),
		row({ id: "stale-note", noteModifiedAt: "2026-01-05T00:00:00Z" }),
		row({ id: "no-date-at-all" }),
	];

	it("hides rows older than the cutoff and COUNTS them", () => {
		const view = partitionTasks(rows, {
			ownerFilter: "all",
			ownerAliases: [],
			cutoff: "2026-06-01",
			now: NOW,
		});
		// The old due date and the stale note both fall before the cutoff.
		expect(view.hiddenByCutoff).toBe(2);
		expect(view.visible).toBe(2);
	});

	/**
	 * A row with no date at all is never hidden. "We do not know how old this
	 * is" is not the claim "it is older than the cutoff", and hiding it would
	 * lose work with nothing to show for it.
	 */
	it("never hides a row it cannot date", () => {
		const view = partitionTasks([row({ id: "unknown" })], {
			ownerFilter: "all",
			ownerAliases: [],
			cutoff: "2026-06-01",
			now: NOW,
		});
		expect(view.hiddenByCutoff).toBe(0);
		expect(view.visible).toBe(1);
	});

	it("showHidden reveals them while still reporting the count", () => {
		const view = partitionTasks(rows, {
			ownerFilter: "all",
			ownerAliases: [],
			cutoff: "2026-06-01",
			showHidden: true,
			now: NOW,
		});
		expect(view.hiddenByCutoff).toBe(2);
		expect(view.visible).toBe(4);
	});

	it("hides nothing with no cutoff set", () => {
		const view = partitionTasks(rows, { ownerFilter: "all", ownerAliases: [], now: NOW });
		expect(view.hiddenByCutoff).toBe(0);
		expect(view.visible).toBe(4);
	});
});
