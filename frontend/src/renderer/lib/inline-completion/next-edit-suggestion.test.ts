import { describe, expect, test } from "vitest";
import { suggestionFrom, type WindowView } from "./next-edit-suggestion";

const view = (
	current: string[],
	cursor: [line: number, column: number],
	original = current,
	after: string[] = [],
): WindowView => ({
	current: current.join("\n"),
	original: original.join("\n"),
	firstLineNumber: 10,
	lineNumber: cursor[0],
	column: cursor[1],
	after,
});

describe("suggestionFrom", () => {
	test("nothing when the model kept the window, or changed only whitespace", () => {
		const v = view(["a", "b"], [10, 2]);
		expect(suggestionFrom(v, "a\nb")).toBeNull();
		expect(suggestionFrom(v, "a\nb\n")).toBeNull();
		expect(suggestionFrom(v, "a \n  b")).toBeNull();
	});

	test("nothing when the model only puts back what the person just changed", () => {
		const v = view(["func greet(name: String) {"], [10, 26], ["func greet() {"]);
		expect(suggestionFrom(v, "func greet() {")).toBeNull();
	});

	test("an insertion at the cursor is ghost text", () => {
		const v = view(["\tlet promo", "}"], [10, 11]);
		expect(suggestionFrom(v, "\tlet promotionTitle = offersTitle\n}")).toEqual({
			kind: "insert",
			text: "tionTitle = offersTitle",
		});
	});

	test("a multi-line insertion at the cursor is ghost text too", () => {
		const v = view(["if ok {", "}"], [10, 8]);
		expect(suggestionFrom(v, "if ok {\n\trun()\n}")).toEqual({ kind: "insert", text: "\n\trun()" });
	});

	test("an insertion trimmed to the wrong side of a repeated character still lands at the cursor", () => {
		// "ab|" + "b": trimming finds the insertion before the existing b.
		const v = view(["ab", "x"], [10, 3]);
		expect(suggestionFrom(v, "abb\nx")).toEqual({ kind: "insert", text: "b" });
	});

	test("a change below the cursor is an edit at its own location", () => {
		const v = view(
			["private func didSelect(offer: Offer, at position: Int) {", "\tguard index < offers.count else { return }"],
			[10, 55],
		);
		expect(
			suggestionFrom(
				v,
				"private func didSelect(offer: Offer, at position: Int) {\n\tguard position < offers.count else { return }",
			),
		).toEqual({
			kind: "edit",
			startLineNumber: 11,
			startColumn: 8,
			endLineNumber: 11,
			endColumn: 13,
			text: "position",
		});
	});

	test("a change above the cursor is an edit too", () => {
		const v = view(["let a = old", "", "use(new)"], [12, 9]);
		expect(suggestionFrom(v, "let a = new\n\nuse(new)")).toMatchObject({
			kind: "edit",
			startLineNumber: 10,
			text: "new",
		});
	});

	test("an insertion away from the cursor is an edit, not ghost text", () => {
		const v = view(["a", "b"], [10, 2]);
		expect(suggestionFrom(v, "a\nb!")).toMatchObject({
			kind: "edit",
			startLineNumber: 11,
			startColumn: 2,
			endColumn: 2,
			text: "!",
		});
	});

	test("two changes in the window become one edit spanning both", () => {
		const v = view(["x = 1", "y", "z = 1"], [11, 2]);
		expect(suggestionFrom(v, "x = 2\ny\nz = 2")).toEqual({
			kind: "edit",
			startLineNumber: 10,
			startColumn: 5,
			endLineNumber: 12,
			endColumn: 6,
			text: "2\ny\nz = 2",
		});
	});

	test("lines the rewrite spills past the window, that the file already has there, are dropped", () => {
		const v = view(["a", "b", "c"], [10, 2], undefined, ["d", "e"]);
		expect(suggestionFrom(v, "a\nb\nc\nd\ne")).toBeNull();
		expect(suggestionFrom(v, "a\nB\nc\nd")).toMatchObject({ kind: "edit", text: "B" });
	});

	test("lines added at the end that the file does NOT have next are a real suggestion", () => {
		const v = view(["a", "b", "c"], [10, 2], undefined, ["d"]);
		expect(suggestionFrom(v, "a\nb\nc\nx")).toMatchObject({ kind: "edit", startLineNumber: 12, text: "\nx" });
	});
});
