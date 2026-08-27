import { describe, expect, it } from "vitest";
import {
	type FileHistory,
	backTarget,
	canGoBack,
	canGoForward,
	currentEntry,
	emptyHistory,
	entryToOpen,
	forwardTarget,
	goBack,
	goForward,
	markPosition,
	pushHistory,
} from "./file-history";

const paths = (history: FileHistory) => history.entries.map((e) => e.path);

describe("pushHistory", () => {
	it("starts empty and goes nowhere", () => {
		expect(canGoBack(emptyHistory)).toBe(false);
		expect(canGoForward(emptyHistory)).toBe(false);
		expect(currentEntry(emptyHistory)).toBeNull();
	});

	it("records each jump and lands on the newest", () => {
		let h = pushHistory(emptyHistory, { path: "a.ts" });
		h = pushHistory(h, { path: "b.ts" });
		expect(paths(h)).toEqual(["a.ts", "b.ts"]);
		expect(currentEntry(h)?.path).toBe("b.ts");
		expect(canGoBack(h)).toBe(true);
		expect(canGoForward(h)).toBe(false);
	});

	// Clicking the same rail row twice, or ⌘⇧O onto the file already open. A stack
	// full of duplicates makes Back do nothing visible, which reads as broken.
	it("does not record landing where you already are", () => {
		let h = pushHistory(emptyHistory, { path: "a.ts" });
		h = pushHistory(h, { path: "a.ts" });
		h = pushHistory(h, { path: "a.ts", line: undefined });
		expect(paths(h)).toEqual(["a.ts"]);
	});

	// ⌘click on a symbol defined thirty lines up is a jump like any other, and
	// has to be reversible.
	it("records a jump WITHIN a file when the line differs", () => {
		let h = pushHistory(emptyHistory, { path: "a.ts", line: 10 });
		h = pushHistory(h, { path: "a.ts", line: 40 });
		expect(h.entries.map((e) => e.line)).toEqual([10, 40]);
		h = pushHistory(h, { path: "a.ts", line: 40 });
		expect(h.entries).toHaveLength(2);
	});

	it("truncates forward when you jump from the middle", () => {
		let h = pushHistory(pushHistory(pushHistory(emptyHistory, { path: "a.ts" }), { path: "b.ts" }), { path: "c.ts" });
		h = goBack(h);
		expect(currentEntry(h)?.path).toBe("b.ts");
		h = pushHistory(h, { path: "d.ts" });
		expect(paths(h)).toEqual(["a.ts", "b.ts", "d.ts"]);
		expect(canGoForward(h)).toBe(false);
	});

	it("keeps the newest 50 entries", () => {
		let h = emptyHistory;
		for (let i = 0; i < 60; i++) h = pushHistory(h, { path: `f${i}.ts` });
		expect(h.entries).toHaveLength(50);
		expect(h.entries[0].path).toBe("f10.ts");
		expect(currentEntry(h)?.path).toBe("f59.ts");
	});
});

describe("back and forward", () => {
	const three = () =>
		pushHistory(pushHistory(pushHistory(emptyHistory, { path: "a.ts" }), { path: "b.ts" }), { path: "c.ts" });

	it("walks backwards and forwards over the same entries", () => {
		let h = three();
		expect(backTarget(h)?.path).toBe("b.ts");
		h = goBack(h);
		h = goBack(h);
		expect(currentEntry(h)?.path).toBe("a.ts");
		expect(canGoBack(h)).toBe(false);
		expect(forwardTarget(h)?.path).toBe("b.ts");
		h = goForward(h);
		expect(currentEntry(h)?.path).toBe("b.ts");
	});

	it("is a no-op at either end", () => {
		const h = three();
		expect(goForward(h)).toBe(h);
		expect(goBack(goBack(goBack(h)))).toEqual(goBack(goBack(h)));
	});
});

describe("the departure position", () => {
	// The difference between Back going SOMEWHERE and Back going where you
	// expected: ⌘click from line 400 has to come back to line 400, not to line 1.
	it("returns to the line you jumped from, not the top of the file", () => {
		let h = pushHistory(emptyHistory, { path: "a.ts" });
		h = pushHistory(h, { path: "b.ts", line: 3 }, { path: "a.ts", line: 400, column: 7 });
		h = goBack(h);
		expect(currentEntry(h)).toMatchObject({ path: "a.ts", line: 400, column: 7 });
	});

	// A cursor report that names a different file than the current entry is stale
	// — it arrived from a viewer that has already moved on.
	it("ignores a report for a different file", () => {
		const h = pushHistory(emptyHistory, { path: "a.ts", line: 5 });
		expect(markPosition(h, { path: "z.ts", line: 99 })).toBe(h);
		expect(currentEntry(h)?.line).toBe(5);
	});

	it("leaves the history untouched when the cursor has not moved", () => {
		const h = pushHistory(emptyHistory, { path: "a.ts", line: 5 });
		expect(markPosition(h, { path: "a.ts", line: 5 })).toBe(h);
	});
});

describe("entryToOpen", () => {
	// "Land on what this branch changed" is a rule for a Changes row's FIRST
	// open. Coming BACK to that file means the line you left, not a re-run of it.
	it("drops first-hunk focus once the entry knows a line", () => {
		expect(entryToOpen({ path: "a.ts", focus: "first-hunk" }).focus).toBe("first-hunk");
		expect(entryToOpen({ path: "a.ts", line: 88, focus: "first-hunk" }).focus).toBeUndefined();
	});

	// Back/forward is not a gesture about where the file LIVES, so it follows
	// quietly rather than taking the rail.
	it("always follows rather than focuses", () => {
		expect(entryToOpen({ path: "a.ts" }).reveal).toBe("follow");
	});

	it("carries the server's containment verdict, never re-deriving it", () => {
		expect(entryToOpen({ path: "/etc/hosts", inWorkspace: false }).inWorkspace).toBe(false);
	});
});
