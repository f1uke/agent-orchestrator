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
