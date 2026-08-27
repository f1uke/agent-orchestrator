import { describe, expect, test } from "vitest";
import type { editor, IPosition } from "monaco-editor";
import {
	asCompletionList,
	defaultRange,
	documentationOf,
	type LspCompletionItem,
	monacoKind,
	toMonacoCompletionItem,
} from "./completion-mapping";

/**
 * Every case here is one that FAILS SILENTLY in the app: nothing throws, the
 * widget still opens, and the row is simply wrong. The payloads are the real
 * ones both servers sent while this slice was being measured.
 */

const POSITION: IPosition = { lineNumber: 31, column: 20 };

/** Just enough model for `defaultRange`; a real one would boot the editor. */
function model(word: { startColumn: number; endColumn: number; word: string }): editor.ITextModel {
	return {
		getWordUntilPosition: () => ({ word: word.word, startColumn: word.startColumn, endColumn: POSITION.column }),
		getWordAtPosition: () => ({ word: word.word, startColumn: word.startColumn, endColumn: word.endColumn }),
	} as unknown as editor.ITextModel;
}

const FALLBACK = defaultRange(model({ startColumn: 20, endColumn: 20, word: "" }), POSITION);

describe("kind translation", () => {
	// The whole reason this table exists: the two enums are not the same list.
	test("LSP Method (2) is Monaco Method (0), not Monaco Constructor", () => {
		expect(monacoKind(2)).toBe(0);
	});

	test("LSP Text (1) is Monaco Text (18), which a cast would have made Method", () => {
		expect(monacoKind(1)).toBe(18);
	});

	test("LSP Property (10) is Monaco Property (9)", () => {
		expect(monacoKind(10)).toBe(9);
	});

	test("LSP Struct (22) is Monaco Struct (6), not Monaco Customcolor (22)", () => {
		expect(monacoKind(22)).toBe(6);
	});

	test("a kind the server invented, and a missing one, both land on Property", () => {
		expect(monacoKind(99)).toBe(9);
		expect(monacoKind(undefined)).toBe(9);
	});

	test("no LSP kind maps to itself by accident across the whole range", () => {
		// If it did, a naive `kind: item.kind` would have passed the tests above.
		const identical = [...Array(25).keys()].map((i) => i + 1).filter((lsp) => monacoKind(lsp) === lsp);
		expect(identical).toEqual([]);
	});
});

describe("insert text", () => {
	test("insertTextFormat 2 is a snippet, or ${1:} is typed into the buffer literally", () => {
		const item: LspCompletionItem = {
			// gopls, verbatim.
			label: "Abs",
			insertTextFormat: 2,
			textEdit: {
				newText: "Abs(${1:})",
				insert: { start: { line: 30, character: 19 }, end: { line: 30, character: 19 } },
				replace: { start: { line: 30, character: 19 }, end: { line: 30, character: 19 } },
			},
		};
		const mapped = toMonacoCompletionItem(item, POSITION, FALLBACK);
		expect(mapped.insertText).toBe("Abs(${1:})");
		expect(mapped.insertTextRules).toBe(4);
	});

	test("plain text carries no snippet rule", () => {
		const mapped = toMonacoCompletionItem({ label: "inputAssistantItem", insertTextFormat: 1 }, POSITION, FALLBACK);
		expect(mapped.insertTextRules).toBe(0);
	});

	test("with no edit and no insertText the label is inserted", () => {
		expect(toMonacoCompletionItem({ label: "viewDidLoad" }, POSITION, FALLBACK).insertText).toBe("viewDidLoad");
	});
});

describe("ranges", () => {
	test("an InsertReplaceEdit becomes Monaco's two-range shape", () => {
		const mapped = toMonacoCompletionItem(
			{
				label: "DiscoverEntry",
				textEdit: {
					newText: "DiscoverEntry",
					insert: { start: { line: 30, character: 15 }, end: { line: 30, character: 19 } },
					replace: { start: { line: 30, character: 15 }, end: { line: 30, character: 25 } },
				},
			},
			POSITION,
			FALLBACK,
		);
		expect(mapped.range).toEqual({
			insert: { startLineNumber: 31, startColumn: 16, endLineNumber: 31, endColumn: 20 },
			replace: { startLineNumber: 31, startColumn: 16, endLineNumber: 31, endColumn: 26 },
		});
	});

	test("a plain TextEdit becomes one range, 0-based to 1-based", () => {
		const mapped = toMonacoCompletionItem(
			{
				label: "inputAssistantItem",
				textEdit: {
					newText: "inputAssistantItem",
					range: { start: { line: 30, character: 19 }, end: { line: 30, character: 19 } },
				},
			},
			POSITION,
			FALLBACK,
		);
		expect(mapped.range).toEqual({ startLineNumber: 31, startColumn: 20, endLineNumber: 31, endColumn: 20 });
	});

	// 🗝 Monaco applies a bad range anyway, eating text around the cursor.
	test("a range that does NOT contain the requested position falls back", () => {
		const mapped = toMonacoCompletionItem(
			{
				label: "x",
				textEdit: { newText: "x", range: { start: { line: 30, character: 2 }, end: { line: 30, character: 5 } } },
			},
			POSITION,
			FALLBACK,
		);
		expect(mapped.range).toEqual(FALLBACK);
	});

	test("a multi-line range falls back", () => {
		const mapped = toMonacoCompletionItem(
			{
				label: "x",
				textEdit: { newText: "x", range: { start: { line: 30, character: 19 }, end: { line: 33, character: 1 } } },
			},
			POSITION,
			FALLBACK,
		);
		expect(mapped.range).toEqual(FALLBACK);
	});

	test("a range on a different line falls back", () => {
		const mapped = toMonacoCompletionItem(
			{
				label: "x",
				textEdit: { newText: "x", range: { start: { line: 12, character: 0 }, end: { line: 12, character: 4 } } },
			},
			POSITION,
			FALLBACK,
		);
		expect(mapped.range).toEqual(FALLBACK);
	});

	test("an InsertReplaceEdit falls back if EITHER half is unusable", () => {
		const mapped = toMonacoCompletionItem(
			{
				label: "x",
				textEdit: {
					newText: "x",
					insert: { start: { line: 30, character: 15 }, end: { line: 30, character: 19 } },
					replace: { start: { line: 30, character: 15 }, end: { line: 31, character: 2 } },
				},
			},
			POSITION,
			FALLBACK,
		);
		expect(mapped.range).toEqual(FALLBACK);
	});

	test("the fallback replaces to the end of the word the cursor sits inside", () => {
		const range = defaultRange(model({ startColumn: 15, endColumn: 26, word: "Discover" }), POSITION);
		expect(range.insert.endColumn).toBe(20);
		expect(range.replace.endColumn).toBe(26);
	});
});

describe("the rest of the item", () => {
	test("labelDetails become Monaco's structured label", () => {
		const mapped = toMonacoCompletionItem(
			{ label: "configure", labelDetails: { detail: "(userDefaultManager:)", description: "Void" } },
			POSITION,
			FALLBACK,
		);
		expect(mapped.label).toEqual({ label: "configure", detail: "(userDefaultManager:)", description: "Void" });
	});

	test("a bare label stays a string", () => {
		expect(toMonacoCompletionItem({ label: "configure" }, POSITION, FALLBACK).label).toBe("configure");
	});

	test("sourcekit-lsp's numeric sortText is carried verbatim", () => {
		const mapped = toMonacoCompletionItem(
			{ label: "inputAssistantItem", sortText: "4998.58274688-inputAssistantItem" },
			POSITION,
			FALLBACK,
		);
		expect(mapped.sortText).toBe("4998.58274688-inputAssistantItem");
	});

	test("the deprecated TAG and the deprecated FLAG both reach Monaco", () => {
		expect(toMonacoCompletionItem({ label: "a", tags: [1] }, POSITION, FALLBACK).tags).toEqual([1]);
		expect(toMonacoCompletionItem({ label: "a", deprecated: true }, POSITION, FALLBACK).tags).toEqual([1]);
		expect(toMonacoCompletionItem({ label: "a" }, POSITION, FALLBACK).tags).toBeUndefined();
	});

	test("markdown documentation is markdown; plaintext is NOT handed over as markdown", () => {
		expect(documentationOf({ label: "a", documentation: { kind: "markdown", value: "**b**" } })).toEqual({
			value: "**b**",
			isTrusted: false,
		});
		expect(documentationOf({ label: "a", documentation: { kind: "plaintext", value: "a_b_c" } })).toBe("a_b_c");
		expect(documentationOf({ label: "a", documentation: "plain" })).toBe("plain");
		expect(documentationOf({ label: "a" })).toBeUndefined();
	});

	test("additionalTextEdits keep their own ranges", () => {
		const mapped = toMonacoCompletionItem(
			{
				label: "Abs",
				additionalTextEdits: [
					{
						range: { start: { line: 4, character: 0 }, end: { line: 4, character: 0 } },
						newText: '\t"path/filepath"\n',
					},
				],
			},
			POSITION,
			FALLBACK,
		);
		expect(mapped.additionalTextEdits).toEqual([
			{ range: { startLineNumber: 5, startColumn: 1, endLineNumber: 5, endColumn: 1 }, text: '\t"path/filepath"\n' },
		]);
	});
});

describe("the response envelope", () => {
	test("a bare array is a complete list", () => {
		expect(asCompletionList([{ label: "a" }])).toEqual({ isIncomplete: false, items: [{ label: "a" }] });
	});

	test("isIncomplete is carried, never assumed", () => {
		expect(asCompletionList({ isIncomplete: true, items: [] }).isIncomplete).toBe(true);
		expect(asCompletionList({ items: [{ label: "a" }] }).isIncomplete).toBe(false);
	});

	test("null is an empty list, not a crash", () => {
		expect(asCompletionList(null)).toEqual({ isIncomplete: false, items: [] });
	});
});
