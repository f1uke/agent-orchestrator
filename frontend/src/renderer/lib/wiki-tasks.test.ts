import { describe, expect, it } from "vitest";
import type { WikiTaskRow } from "../hooks/useWiki";
import { isMine, partitionTasks, rowDate } from "./wiki-tasks";

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

describe("rowDate", () => {
	it("prefers the row's own created: date", () => {
		expect(rowDate(row({ created: "2026-08-20", fromDate: "2026-05-07" }))).toBe("2026-08-20");
	});

	it("falls back to the date in the (from: …) tag", () => {
		expect(rowDate(row({ fromDate: "2026-05-07" }))).toBe("2026-05-07");
	});

	/**
	 * 🗝 A due date is a promise about the future, not a record of when the row
	 * was written, so it does not answer "how old is this". The cutoff asks the
	 * second question; the day grouping asks the first.
	 */
	it("is not the due date", () => {
		expect(rowDate(row({ due: "2026-01-02" }))).toBeNull();
	});

	it("is null when the row carries no date of its own", () => {
		expect(rowDate(row())).toBeNull();
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
	 * The rule #293 committed to and this change keeps: grouping keys on `due:`
	 * ALONE. A creation date is not a promise, so a row dated only by when it
	 * was captured belongs in the undated group, not under some past day.
	 */
	it("never groups a row by the date it was created", () => {
		const view = partitionTasks([row({ created: "2026-08-20", fromDate: "2026-05-07" })], {
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
		row({ id: "old-created", created: "2026-01-01" }),
		row({ id: "old-from", fromDate: "2026-01-05" }),
		row({ id: "recent", created: "2026-09-01" }),
		row({ id: "no-date-at-all" }),
	];

	it("hides rows older than the cutoff and COUNTS them", () => {
		const view = partitionTasks(rows, {
			ownerFilter: "all",
			ownerAliases: [],
			cutoff: "2026-06-01",
			now: NOW,
		});
		expect(view.hiddenByCutoff).toBe(2);
		expect(view.visible).toBe(2);
	});

	/**
	 * A row with no date at all is never hidden. "We do not know how old this
	 * is" is not the claim "it is older than the cutoff", and hiding it would
	 * lose work with nothing to show for it. It is COUNTED, so the tab can say
	 * out loud that the cutoff left it here.
	 */
	it("never hides a row it cannot date, and says how many those are", () => {
		const view = partitionTasks([row({ id: "unknown" }), row({ id: "due-only", due: "2026-01-01" })], {
			ownerFilter: "all",
			ownerAliases: [],
			cutoff: "2026-06-01",
			now: NOW,
		});
		expect(view.hiddenByCutoff).toBe(0);
		expect(view.undated).toBe(2);
		expect(view.visible).toBe(2);
	});

	it("counts only the rows the owner filter left", () => {
		const view = partitionTasks([row({ id: "mine" }), row({ id: "theirs", owner: "Someone" })], {
			ownerFilter: "mine",
			ownerAliases: ["Fluke"],
			cutoff: "2026-06-01",
			now: NOW,
		});
		expect(view.undated).toBe(1);
		expect(view.hiddenByOwner).toBe(1);
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

/**
 * `requireCreated` is the reader deliberately overriding the rule above: in a
 * vault where `created:` is written at capture time, an untagged row is not
 * "undatable", it is old.
 *
 * 🗝 It only ever HIDES. The count is still reported and `showHidden` still
 * brings every row back, so the property the tab exists to hold — a filtered
 * list can never be mistaken for a destroyed backlog — survives the override.
 */
describe("partitionTasks requireCreated", () => {
	const mixed = [
		row({ id: "tagged", created: "2026-09-03" }),
		row({ id: "tagged-old", created: "2026-01-01" }),
		row({ id: "from-only", fromDate: "2026-09-03" }),
		row({ id: "bare" }),
	];

	it("keeps only the rows carrying a created: date, and counts the rest", () => {
		const view = partitionTasks(mixed, { ownerFilter: "all", ownerAliases: [], requireCreated: true, now: NOW });
		expect(view.visible).toBe(2);
		expect(view.undated).toBe(2);
		expect(view.undatedHidden).toBe(true);
		expect(view.groups.flatMap((g) => g.rows.map((r) => r.id)).sort()).toEqual(["tagged", "tagged-old"]);
	});

	/**
	 * A `(from: …)` date says when the CONVERSATION was, not when the row was
	 * taken on. Under this rule it stops standing in for `created:` — a row
	 * carrying only provenance is judged untagged, not judged by the wrong day.
	 */
	it("stops a (from: …) date standing in for created:", () => {
		expect(rowDate(row({ fromDate: "2026-05-07" }), true)).toBeNull();
		expect(rowDate(row({ created: "2026-08-20", fromDate: "2026-05-07" }), true)).toBe("2026-08-20");
		const view = partitionTasks([row({ id: "from-only", fromDate: "2026-09-03" })], {
			ownerFilter: "all",
			ownerAliases: [],
			cutoff: "2026-06-01",
			requireCreated: true,
			now: NOW,
		});
		// Hidden for having no created:, NOT counted as hidden by the cutoff —
		// the cutoff never got to judge it.
		expect(view.visible).toBe(0);
		expect(view.hiddenByCutoff).toBe(0);
		expect(view.undated).toBe(1);
	});

	it("still applies the cutoff to the rows it did keep", () => {
		const view = partitionTasks(mixed, {
			ownerFilter: "all",
			ownerAliases: [],
			cutoff: "2026-06-01",
			requireCreated: true,
			now: NOW,
		});
		expect(view.visible).toBe(1);
		expect(view.hiddenByCutoff).toBe(1);
		expect(view.undated).toBe(2);
	});

	it("gives every row back under showHidden, both kinds at once", () => {
		const view = partitionTasks(mixed, {
			ownerFilter: "all",
			ownerAliases: [],
			cutoff: "2026-06-01",
			requireCreated: true,
			showHidden: true,
			now: NOW,
		});
		expect(view.visible).toBe(4);
		expect(view.hiddenByCutoff).toBe(1);
		expect(view.undated).toBe(2);
	});

	it("changes nothing at all when it is off", () => {
		const view = partitionTasks(mixed, { ownerFilter: "all", ownerAliases: [], now: NOW });
		expect(view.visible).toBe(4);
		expect(view.undatedHidden).toBe(false);
	});
});
