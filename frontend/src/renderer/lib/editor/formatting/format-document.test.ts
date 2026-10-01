import { describe, expect, it, vi } from "vitest";
import type { FormatResult } from "../../../../main/format/formatters";
import { applyLspEdits, fitToBuffer, type FormatInput, formatText, type LspFormatter } from "./format-document";

const GO = "package main\nfunc main(){\nx()\n}\n";

function input(overrides: Partial<FormatInput> = {}): FormatInput {
	return {
		languageId: "go",
		languageName: "Go",
		path: "main.go",
		absolutePath: "/repo/main.go",
		workspaceRoot: "/repo",
		text: GO,
		unit: { insertSpaces: false, indentSize: 4, tabSize: 4 },
		lsp: null,
		runTool: null,
		...overrides,
	};
}

function lsp(answer: () => Promise<unknown>, serverText = GO): LspFormatter {
	return { name: "gopls", serverText: () => serverText, request: answer as LspFormatter["request"] };
}

describe("formatText", () => {
	it("asks the language server first and applies its edits", async () => {
		const runTool = vi.fn();
		const outcome = await formatText(
			input({
				lsp: lsp(async () => [
					{ range: { start: { line: 1, character: 11 }, end: { line: 1, character: 11 } }, newText: " " },
				]),
				runTool,
			}),
		);
		expect(outcome).toEqual({ kind: "formatted", text: "package main\nfunc main() {\nx()\n}\n", formatter: "gopls" });
		expect(runTool).not.toHaveBeenCalled();
	});

	it("treats a null LSP answer as already formatted", async () => {
		expect(await formatText(input({ lsp: lsp(async () => null) }))).toEqual({
			kind: "formatted",
			text: GO,
			formatter: "gopls",
		});
	});

	it("never asks a server that holds different text from the buffer", async () => {
		const request = vi.fn(async () => []);
		const runTool = vi.fn(async (): Promise<FormatResult> => ({ ok: true, text: "formatted", formatter: "gofmt" }));
		const outcome = await formatText(input({ lsp: { name: "gopls", serverText: () => "stale", request }, runTool }));
		expect(request).not.toHaveBeenCalled();
		expect(outcome).toMatchObject({ formatter: "gofmt" });
	});

	it("falls back to the tool when the server fails or times out", async () => {
		const runTool = vi.fn(async (): Promise<FormatResult> => ({ ok: true, text: "formatted", formatter: "gofmt" }));
		expect(await formatText(input({ lsp: lsp(async () => Promise.reject(new Error("boom"))), runTool }))).toMatchObject(
			{
				formatter: "gofmt",
			},
		);
		const never = lsp(() => new Promise(() => undefined));
		expect(await formatText(input({ lsp: never, runTool, lspTimeoutMs: 10 }))).toMatchObject({ formatter: "gofmt" });
	});

	it("stops at a tool that ran and refused, rather than re-indenting instead", async () => {
		const runTool = async (): Promise<FormatResult> => ({
			ok: false,
			reason: "failed",
			formatter: "gofmt",
			message: "main.go:3:1: expected '}'",
		});
		expect(await formatText(input({ runTool }))).toEqual({
			kind: "failed",
			formatter: "gofmt",
			message: "main.go:3:1: expected '}'",
		});
	});

	it("reports the server's failure when there is no tool to try", async () => {
		const runTool = async (): Promise<FormatResult> => ({
			ok: false,
			reason: "unavailable",
			message: "gofmt is not installed",
		});
		expect(
			await formatText(input({ lsp: lsp(async () => Promise.reject(new Error("parse error"))), runTool })),
		).toEqual({
			kind: "failed",
			formatter: "gopls",
			message: "parse error",
		});
	});

	it("re-indents a bracket language with no formatter, and says why", async () => {
		const runTool = async (): Promise<FormatResult> => ({
			ok: false,
			reason: "unavailable",
			formatter: "prettier",
			message: "This project does not configure Prettier.",
		});
		const outcome = await formatText(
			input({
				languageId: "typescript",
				languageName: "TypeScript",
				path: "a.ts",
				text: "if (x) {\ny();\n}\n",
				runTool,
			}),
		);
		expect(outcome).toEqual({
			kind: "formatted",
			text: "if (x) {\n\ty();\n}\n",
			formatter: "re-indent",
			note: "This project does not configure Prettier.",
		});
		expect(
			await formatText(
				input({ languageId: "java", languageName: "Java", path: "A.java", text: "class A {\nint x;\n}\n" }),
			),
		).toMatchObject({ kind: "formatted", note: "There is no formatter for Java files." });
	});

	it("says there is nothing to do for a language it cannot re-indent either", async () => {
		expect(
			await formatText(input({ languageId: "python", languageName: "Python", path: "a.py", text: "x = 1\n" })),
		).toEqual({
			kind: "unsupported",
			message: "There is no formatter for Python files.",
		});
	});
});

describe("fitToBuffer", () => {
	it("drops the final newline a formatter adds to a buffer that holds none", () => {
		// The model holds `package main` for a file whose bytes are `package main\n`.
		expect(fitToBuffer("package main\n", "package main", "\n")).toBe("package main");
		expect(fitToBuffer("a\n", "a\n", "\n")).toBe("a\n");
	});

	it("follows the buffer's line endings", () => {
		expect(fitToBuffer("a\nb\n", "a\r\nb", "\r\n")).toBe("a\r\nb");
		expect(fitToBuffer("a\r\nb\r\n", "a\nb\n", "\n")).toBe("a\nb\n");
	});
});

describe("applyLspEdits", () => {
	it("applies several edits against the original positions", () => {
		const text = "a\nbb\nccc\n";
		expect(
			applyLspEdits(text, [
				{ range: { start: { line: 0, character: 0 }, end: { line: 0, character: 1 } }, newText: "A" },
				{ range: { start: { line: 2, character: 1 }, end: { line: 2, character: 3 } }, newText: "" },
				{ range: { start: { line: 1, character: 0 }, end: { line: 1, character: 0 } }, newText: "\t" },
			]),
		).toBe("A\n\tbb\nc\n");
	});

	it("never lands inside a CRLF line break", () => {
		expect(
			applyLspEdits("a\r\nb\r\n", [
				{ range: { start: { line: 0, character: 5 }, end: { line: 0, character: 5 } }, newText: ";" },
			]),
		).toBe("a;\r\nb\r\n");
	});
});
