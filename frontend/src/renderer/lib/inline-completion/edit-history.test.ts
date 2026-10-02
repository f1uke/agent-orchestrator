import { describe, expect, test } from "vitest";
import { EditHistory, type LineChange } from "./edit-history";

const FILE = ["func greet() {", '\tprint("hi")', "}", "", "greet()"].join("\n");

/** Typing `text` at 0-based (line, column), one character per event, as an editor reports it. */
function type(history: EditHistory, key: string, line: number, column: number, text: string): void {
	for (const [i, ch] of [...text].entries()) {
		const change: LineChange = {
			startLine: line,
			startColumn: column + i,
			endLine: line,
			endColumn: column + i,
			text: ch,
		};
		history.apply(key, [change]);
	}
}

describe("EditHistory", () => {
	test("keystrokes close together are ONE change, with the region before and after", () => {
		const h = new EditHistory();
		h.track("a", "greet.swift", FILE);
		type(h, "a", 0, 11, "name: String");
		expect(h.recent()).toEqual([
			{
				path: "greet.swift",
				original: ["func greet() {", '\tprint("hi")', "}"].join("\n"),
				updated: ["func greet(name: String) {", '\tprint("hi")', "}"].join("\n"),
			},
		]);
		expect(h.lines("a")?.[0]).toBe("func greet(name: String) {");
	});

	test("the window before the latest change swaps the changed region back", () => {
		const h = new EditHistory();
		h.track("a", "greet.swift", FILE);
		type(h, "a", 0, 11, "name: String");
		expect(h.windowBeforeLatest("a", 0, 4)).toEqual(FILE.split("\n"));
	});

	test("a change far from the last one starts a new change; the old one keeps its text", () => {
		const h = new EditHistory();
		const long = Array.from({ length: 40 }, (_, i) => `line ${i}`).join("\n");
		h.track("a", "f.ts", long);
		type(h, "a", 2, 6, "X");
		type(h, "a", 30, 7, "Y");
		const recent = h.recent();
		expect(recent).toHaveLength(2);
		expect(recent[0].updated).toContain("line 2X");
		expect(recent[1].updated).toContain("line 30Y");
		// Before the LATEST change only: line 2's edit stays.
		expect(h.windowBeforeLatest("a", 0, 3)).toEqual(["line 0", "line 1", "line 2X", "line 3"]);
		expect(h.windowBeforeLatest("a", 29, 31)).toEqual(["line 29", "line 30", "line 31"]);
	});

	test("a change that came to nothing (typed, then deleted) is not reported", () => {
		const h = new EditHistory();
		h.track("a", "greet.swift", FILE);
		type(h, "a", 4, 7, "x");
		h.apply("a", [{ startLine: 4, startColumn: 7, endLine: 4, endColumn: 8, text: "" }]);
		expect(h.recent()).toEqual([]);
	});

	test("lines inserted and removed keep the region aligned", () => {
		const h = new EditHistory();
		h.track("a", "greet.swift", FILE);
		// Enter at the end of line 1, then type the new line.
		h.apply("a", [{ startLine: 1, startColumn: 12, endLine: 1, endColumn: 12, text: "\n\t" }]);
		type(h, "a", 2, 1, "return");
		expect(h.lines("a")).toEqual(["func greet() {", '\tprint("hi")', "\treturn", "}", "", "greet()"]);
		const [change] = h.recent();
		expect(change.updated).toBe(["func greet() {", '\tprint("hi")', "\treturn", "}", ""].join("\n"));
		expect(change.original).toBe(["func greet() {", '\tprint("hi")', "}", ""].join("\n"));
		expect(h.windowBeforeLatest("a", 0, 5)).toEqual(FILE.split("\n"));
	});

	test("changes across files are kept in order, at most three", () => {
		const h = new EditHistory();
		for (const k of ["a", "b", "c", "d"]) h.track(k, `${k}.ts`, "one\ntwo");
		for (const k of ["a", "b", "c", "d"]) type(h, k, 0, 3, "!");
		expect(h.recent().map((c) => c.path)).toEqual(["b.ts", "c.ts", "d.ts"]);
	});

	test("re-tracking (the text replaced from outside) forgets coordinates but keeps what was said", () => {
		const h = new EditHistory();
		h.track("a", "greet.swift", FILE);
		type(h, "a", 0, 11, "n");
		h.track("a", "greet.swift", "something else");
		expect(h.recent()).toHaveLength(1);
		expect(h.windowBeforeLatest("a", 0, 0)).toEqual(["something else"]);
	});

	test("a multi-change event (end of file first, as Monaco lists them) applies cleanly", () => {
		const h = new EditHistory();
		h.track("a", "f.ts", "a\nb\nc");
		h.apply("a", [
			{ startLine: 2, startColumn: 0, endLine: 2, endColumn: 0, text: "// " },
			{ startLine: 0, startColumn: 0, endLine: 0, endColumn: 0, text: "// " },
		]);
		expect(h.lines("a")).toEqual(["// a", "b", "// c"]);
		expect(h.recent()).toHaveLength(1);
		expect(h.recent()[0]).toMatchObject({ original: "a\nb\nc", updated: "// a\nb\n// c" });
	});
});
