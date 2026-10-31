import { describe, expect, it } from "vitest";
import { editView, highlightView, languageOf, readView, writeView, type CodeLine } from "./chat-code";

const hunk = (oldStart: number, newStart: number, lines: string[]) => ({
	oldStart,
	oldLines: lines.filter((l) => !l.startsWith("+")).length,
	newStart,
	newLines: lines.filter((l) => !l.startsWith("-")).length,
	lines,
});

const line = (kind: CodeLine["kind"], num: number, text: string): CodeLine => ({ kind, num, text });

describe("languageOf", () => {
	it.each([
		["internal/webhook/sender.go", "go"],
		["src/a.ts", "typescript"],
		["src/App.tsx", "typescript"],
		["docs/demo-flow.svg", "xml"],
		["README.md", "markdown"],
		["notes.unknownext", undefined],
		["Makefile", undefined],
	])("maps %s to %s", (path, want) => {
		expect(languageOf(path)).toBe(want);
	});
});

describe("writeView", () => {
	it("numbers every line of the new file from 1 and marks it added", () => {
		const view = writeView("/work/a.go", "package a\n\nfunc A() {}\n", 40);
		expect(view).toEqual({
			path: "/work/a.go",
			language: "go",
			lines: [line("add", 1, "package a"), line("add", 2, ""), line("add", 3, "func A() {}")],
			hidden: 0,
		});
	});

	it("keeps the first lines up to the limit and counts the rest as hidden", () => {
		const content = Array.from({ length: 50 }, (_, i) => `line ${i + 1}`).join("\n");
		const view = writeView("new.txt", content, 40);
		expect(view.lines).toHaveLength(40);
		expect(view.lines.at(-1)).toEqual(line("add", 40, "line 40"));
		expect(view.hidden).toBe(10);
	});

	it("shows an empty file as no lines", () => {
		expect(writeView("empty.go", "", 40).lines).toEqual([]);
	});
});

describe("editView with a structuredPatch", () => {
	const input = { file_path: "/work/report.go", old_string: "x", new_string: "y" };

	it("takes the file's own line numbers from the hunk, not from 1", () => {
		const patch = [hunk(456, 456, [" func a() {", "-\told()", "+\tnew1()", "+\tnew2()", " }"])];
		const view = editView("/work/report.go", input, { structuredPatch: patch });
		expect(view.language).toBe("go");
		expect(view.lines).toEqual([
			line("context", 456, "func a() {"),
			line("del", 457, "\told()"),
			line("add", 457, "\tnew1()"),
			line("add", 458, "\tnew2()"),
			line("context", 459, "}"),
		]);
	});

	it("numbers a deletion on the old side and an addition on the new side when the hunks drift apart", () => {
		const patch = [hunk(10, 10, ["-a", "-b", "+c", " d"]), hunk(100, 99, [" e", "-f", " g"])];
		const view = editView("x.go", input, { structuredPatch: patch });
		expect(view.lines.map((l) => [l.kind, l.num])).toEqual([
			["del", 10],
			["del", 11],
			["add", 10],
			["context", 11],
			["context", 99],
			["del", 101],
			["context", 100],
		]);
	});

	it("drops the no-newline marker that git puts in a hunk", () => {
		const patch = [hunk(1, 1, ["-a", "\\ No newline at end of file", "+b"])];
		expect(editView("x.go", input, { structuredPatch: patch }).lines).toEqual([
			line("del", 1, "a"),
			line("add", 1, "b"),
		]);
	});
});

describe("editView without a structuredPatch", () => {
	// Old logs and a MultiEdit carry no patch. The file's real line numbers are unknown there,
	// so the numbers count from 1 inside the changed text.
	it("diffs the old and new strings and counts from 1", () => {
		const input = { file_path: "a.go", old_string: "one\ntwo\nthree", new_string: "one\n2\nthree\nfour" };
		expect(editView("a.go", input).lines).toEqual([
			line("context", 1, "one"),
			line("del", 2, "two"),
			line("add", 2, "2"),
			line("context", 3, "three"),
			line("add", 4, "four"),
		]);
	});

	it("falls back the same way for an empty patch", () => {
		const input = { file_path: "a.go", old_string: "a", new_string: "b" };
		expect(editView("a.go", input, { structuredPatch: [] }).lines).toEqual([line("del", 1, "a"), line("add", 1, "b")]);
	});

	it("joins the edits of a MultiEdit", () => {
		const input = {
			file_path: "a.go",
			edits: [
				{ old_string: "a", new_string: "b" },
				{ old_string: "c", new_string: "d" },
			],
		};
		expect(editView("a.go", input).lines.map((l) => l.text)).toEqual(["a", "b", "c", "d"]);
	});
});

describe("readView", () => {
	it("reads the line numbers from the cat -n prefix and keeps tabs in the code", () => {
		const output = "     3\tfunc A() {\n     4\t\treturn 1\n     5\t\n     6\t}";
		expect(readView("/work/a.go", output).lines).toEqual([
			line("context", 3, "func A() {"),
			line("context", 4, "\treturn 1"),
			line("context", 5, ""),
			line("context", 6, "}"),
		]);
	});

	it("stops at the first line without a number, such as a trailing system reminder", () => {
		const output = "     1\ta\n     2\tb\n\n<system-reminder>\nnot code\n</system-reminder>";
		expect(readView("a.txt", output).lines.map((l) => l.text)).toEqual(["a", "b"]);
	});

	it("gives no lines for text that is not numbered", () => {
		expect(readView("a.txt", "just some output").lines).toEqual([]);
	});

	it("caps the lines and counts the rest as hidden", () => {
		const output = Array.from({ length: 30 }, (_, i) => `${String(i + 1).padStart(6)}\tl${i + 1}`).join("\n");
		const view = readView("a.txt", output, 20);
		expect(view.lines).toHaveLength(20);
		expect(view.hidden).toBe(10);
	});
});

describe("highlightView", () => {
	const colourOf = (tokens: { text: string; color?: string }[], text: string) =>
		tokens.find((t) => t.text.trim() === text)?.color;

	it("highlights the whole text, so a line inside a block comment is coloured as a comment", async () => {
		const view = writeView("a.go", "/* start\nfunc inside\n*/\nfunc real() {}", 40);
		const tokens = (await highlightView(view, "dark"))!;
		expect(tokens).toHaveLength(4);
		const comment = tokens[0][0].color;
		expect(comment).toBeDefined();
		expect(tokens[1].every((t) => t.color === comment)).toBe(true);
		expect(colourOf(tokens[3], "func")).not.toBe(comment);
	});

	it("colours a deleted line from the old text and an added line from the new text", async () => {
		const view = {
			path: "a.go",
			language: "go",
			hidden: 0,
			lines: [line("del", 1, "/* gone"), line("del", 2, "func inside"), line("add", 1, "func real() {}")],
		};
		const tokens = (await highlightView(view, "dark"))!;
		const comment = tokens[0][0].color;
		expect(tokens[1].every((t) => t.color === comment)).toBe(true);
		expect(colourOf(tokens[2], "func")).not.toBe(comment);
	});

	it.each([
		["a.ts", "export const a = 1;"],
		["a.tsx", "export const a = 1;"],
		["a.js", "export const a = 1;"],
	])("colours %s, whose grammar ships under another name", async (path, code) => {
		const tokens = await highlightView(writeView(path, code, 40), "dark");
		expect(colourOf(tokens?.[0] ?? [], "export")).toBeDefined();
		expect(new Set(tokens?.[0].map((t) => t.color)).size).toBeGreaterThan(1);
	});

	it("gives nothing for a file whose language is unknown", async () => {
		expect(await highlightView(writeView("notes.unknownext", "a\nb", 40), "dark")).toBeUndefined();
	});
});
