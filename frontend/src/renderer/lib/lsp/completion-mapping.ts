import type { editor, IPosition, IRange, languages } from "monaco-editor";

/**
 * LSP completion items → Monaco completion items.
 *
 * Kept apart from the provider, and free of the monaco BARREL (which boots the
 * whole editor on import), for the reason `definition-mapping.ts` gives: this is
 * where the off-by-ones and the enum translations live, and all of them fail
 * SILENTLY — the wrong icon on every row, a literal `${1:}` typed into the
 * buffer, an insert that eats the character before the cursor. None of them
 * throws, so each one is pinned by a test that can run without a DOM.
 */

/**
 * 🗝 The two `CompletionItemKind` enums are DIFFERENT and neither is a shift of
 * the other. LSP counts from 1 with `Text` first, so `Method` is 2; Monaco counts
 * from 0 with `Method` first, so `Text` is 18. `kind: item.kind` compiles, runs,
 * and puts the wrong icon on every single row.
 *
 * The numbers are written out rather than read off the barrel so this file stays
 * importable without a DOM - and `satisfies` below makes tsc prove, at compile
 * time, that every one of them still matches the enum monaco actually ships.
 */
type MonacoKindNumbers = {
	[K in keyof typeof languages.CompletionItemKind]: (typeof languages.CompletionItemKind)[K];
};

const MONACO_KIND = {
	Method: 0,
	Function: 1,
	Constructor: 2,
	Field: 3,
	Variable: 4,
	Class: 5,
	Struct: 6,
	Interface: 7,
	Module: 8,
	Property: 9,
	Event: 10,
	Operator: 11,
	Unit: 12,
	Value: 13,
	Constant: 14,
	Enum: 15,
	EnumMember: 16,
	Keyword: 17,
	Text: 18,
	Color: 19,
	File: 20,
	Reference: 21,
	Customcolor: 22,
	Folder: 23,
	TypeParameter: 24,
	User: 25,
	Issue: 26,
	Tool: 27,
	Snippet: 28,
} as const satisfies MonacoKindNumbers;

/** `insertText` is a snippet (`CompletionItemInsertTextRule.InsertAsSnippet`). */
const INSERT_AS_SNIPPET: languages.CompletionItemInsertTextRule.InsertAsSnippet = 4;
const INSERT_NONE: languages.CompletionItemInsertTextRule.None = 0;
/** `CompletionItemTag.Deprecated`. */
const TAG_DEPRECATED: languages.CompletionItemTag.Deprecated = 1;

/** LSP `CompletionItemKind` 1-25, in its own order, to Monaco's. */
const KIND: Record<number, languages.CompletionItemKind> = {
	1: MONACO_KIND.Text,
	2: MONACO_KIND.Method,
	3: MONACO_KIND.Function,
	4: MONACO_KIND.Constructor,
	5: MONACO_KIND.Field,
	6: MONACO_KIND.Variable,
	7: MONACO_KIND.Class,
	8: MONACO_KIND.Interface,
	9: MONACO_KIND.Module,
	10: MONACO_KIND.Property,
	11: MONACO_KIND.Unit,
	12: MONACO_KIND.Value,
	13: MONACO_KIND.Enum,
	14: MONACO_KIND.Keyword,
	15: MONACO_KIND.Snippet,
	16: MONACO_KIND.Color,
	17: MONACO_KIND.File,
	18: MONACO_KIND.Reference,
	19: MONACO_KIND.Folder,
	20: MONACO_KIND.EnumMember,
	21: MONACO_KIND.Constant,
	22: MONACO_KIND.Struct,
	23: MONACO_KIND.Event,
	24: MONACO_KIND.Operator,
	25: MONACO_KIND.TypeParameter,
};

export function monacoKind(lspKind: number | undefined): languages.CompletionItemKind {
	if (lspKind === undefined) return MONACO_KIND.Property;
	const mapped = KIND[lspKind];
	// `Property`, not `Text`: an item whose kind we do not know is far more often
	// a member than a word lifted out of the document, and Monaco sorts `Text`
	// below everything else.
	return mapped === undefined ? MONACO_KIND.Property : mapped;
}

export type LspPosition = { line: number; character: number };
export type LspRange = { start: LspPosition; end: LspPosition };
export type LspTextEdit = { range: LspRange; newText: string };
export type LspInsertReplaceEdit = { insert: LspRange; replace: LspRange; newText: string };

export type LspCompletionItem = {
	label: string;
	labelDetails?: { detail?: string; description?: string };
	kind?: number;
	tags?: number[];
	detail?: string;
	documentation?: string | { kind?: string; value: string };
	deprecated?: boolean;
	preselect?: boolean;
	sortText?: string;
	filterText?: string;
	insertText?: string;
	insertTextFormat?: number;
	textEdit?: LspTextEdit | LspInsertReplaceEdit;
	textEditText?: string;
	additionalTextEdits?: LspTextEdit[];
	commitCharacters?: string[];
	data?: unknown;
};

export type LspCompletionList = { isIncomplete?: boolean; items: LspCompletionItem[] };

/** `textDocument/completion` may answer with a bare array. Both shapes are legal. */
export function asCompletionList(
	result: LspCompletionList | LspCompletionItem[] | null | undefined,
): LspCompletionList {
	if (!result) return { isIncomplete: false, items: [] };
	if (Array.isArray(result)) return { isIncomplete: false, items: result };
	return { isIncomplete: result.isIncomplete ?? false, items: result.items ?? [] };
}

function isInsertReplace(edit: LspTextEdit | LspInsertReplaceEdit): edit is LspInsertReplaceEdit {
	return (edit as LspInsertReplaceEdit).insert !== undefined;
}

function toMonacoRange(range: LspRange): IRange {
	return {
		startLineNumber: range.start.line + 1,
		startColumn: range.start.character + 1,
		endLineNumber: range.end.line + 1,
		endColumn: range.end.character + 1,
	};
}

/**
 * 🗝 Monaco's contract, verbatim from `editor.api.d.ts`: a completion item's
 * range "must be a single line and it must CONTAIN the position at which
 * completion has been requested". A language server is under no such obligation,
 * and a range that breaks either rule is APPLIED ANYWAY - eating text around the
 * cursor, with no error.
 *
 * So every server range is checked and, when it fails, the word range Monaco
 * would have chosen for itself is used instead. Ignoring server ranges entirely
 * is not the alternative: honouring `textEdit.range` is what makes Swift's
 * argument-label completions insert correctly at all.
 */
function usableRange(range: IRange, position: IPosition): boolean {
	if (range.startLineNumber !== range.endLineNumber) return false;
	if (range.startLineNumber !== position.lineNumber) return false;
	return range.startColumn <= position.column && range.endColumn >= position.column;
}

export type WordRange = { insert: IRange; replace: IRange };

/** The range Monaco itself would use - the fallback, and the default where a server sends no edit. */
export function defaultRange(model: editor.ITextModel, position: IPosition): WordRange {
	const word = model.getWordUntilPosition(position);
	const wholeWord = model.getWordAtPosition(position);
	const insert: IRange = {
		startLineNumber: position.lineNumber,
		startColumn: word.startColumn,
		endLineNumber: position.lineNumber,
		endColumn: position.column,
	};
	return {
		insert,
		// `replace` runs to the end of the word the cursor is inside, so accepting a
		// suggestion in the middle of an existing identifier replaces it rather than
		// leaving its tail behind.
		replace: { ...insert, endColumn: Math.max(position.column, wholeWord?.endColumn ?? position.column) },
	};
}

export function documentationOf(item: LspCompletionItem): string | languages.CompletionItem["documentation"] {
	const doc = item.documentation;
	if (!doc) return undefined;
	if (typeof doc === "string") return doc;
	if (!doc.value) return undefined;
	// `markdown` is what the client capabilities asked for; anything else is
	// plaintext and must NOT be handed over as markdown, or a doc comment full of
	// underscores and asterisks renders as garbled emphasis.
	return doc.kind === "markdown" ? { value: doc.value, isTrusted: false } : doc.value;
}

export function toMonacoCompletionItem(
	item: LspCompletionItem,
	position: IPosition,
	fallback: WordRange,
): languages.CompletionItem {
	let range: IRange | languages.CompletionItemRanges = fallback;
	let insertText = item.textEditText ?? item.insertText ?? item.label;

	const edit = item.textEdit;
	if (edit) {
		insertText = edit.newText;
		if (isInsertReplace(edit)) {
			// gopls answers with this shape, because the client capabilities declare
			// `insertReplaceSupport`. sourcekit-lsp answers with a plain `TextEdit`.
			const insert = toMonacoRange(edit.insert);
			const replace = toMonacoRange(edit.replace);
			range = usableRange(insert, position) && usableRange(replace, position) ? { insert, replace } : fallback;
		} else {
			const single = toMonacoRange(edit.range);
			range = usableRange(single, position) ? single : fallback;
		}
	}

	const label: string | languages.CompletionItemLabel = item.labelDetails
		? { label: item.label, detail: item.labelDetails.detail, description: item.labelDetails.description }
		: item.label;

	const deprecated = item.deprecated === true || item.tags?.includes(1) === true;

	return {
		label,
		kind: monacoKind(item.kind),
		detail: item.detail,
		documentation: documentationOf(item),
		// Kept VERBATIM. sourcekit-lsp's sortText is a numeric score
		// (`4998.58274688-inputAssistantItem`) carrying its own ranking, and
		// re-deriving one from the label throws that ranking away.
		sortText: item.sortText,
		filterText: item.filterText,
		preselect: item.preselect,
		insertText,
		// 🗝 `insertTextFormat: 2` is a SNIPPET. Without the rule Monaco types
		// `configure(${1:userDefaultManager})` into the buffer, literally.
		insertTextRules: item.insertTextFormat === 2 ? INSERT_AS_SNIPPET : INSERT_NONE,
		range,
		commitCharacters: item.commitCharacters,
		additionalTextEdits: item.additionalTextEdits?.map((e) => ({ range: toMonacoRange(e.range), text: e.newText })),
		tags: deprecated ? [TAG_DEPRECATED] : undefined,
	};
}
