import type { IMarkdownString, IRange, languages } from "monaco-editor";

/**
 * Turning `textDocument/hover` into what Monaco's hover widget renders.
 *
 * Kept apart from `hover-provider.ts` for `definition-mapping.ts`'s reason: that
 * file imports the monaco BARREL, which boots the whole editor, so the shape
 * handling and the off-by-ones would otherwise need a DOM to test.
 */

type LspPosition = { line: number; character: number };
type LspRange = { start: LspPosition; end: LspPosition };

/** `MarkedString` in the spec: a bare string, or code in a named language. */
export type LspMarkedString = string | { language?: string; value?: string };
export type LspMarkupContent = { kind?: string; value?: string };

export type LspHover = {
	contents?: LspMarkupContent | LspMarkedString | LspMarkedString[] | null;
	range?: LspRange;
};

/**
 * Plain text, safe to hand to a markdown renderer.
 *
 * 🗝 Monaco's hover takes `IMarkdownString[]` and NOTHING ELSE — unlike
 * completion's `documentation`, which also accepts a plain string. So the rule
 * #258 established (markdown as markdown, plaintext NOT as markdown) cannot be
 * met here by picking a different type; the text has to be escaped. Without it a
 * doc comment full of underscores and asterisks renders as garbled emphasis, and
 * a line starting with `-` becomes a bullet list.
 *
 * The `\n` → `\n\n` step is the same one VS Code's own `MarkdownString.appendText`
 * makes: markdown folds a single newline into a space, so a multi-line signature
 * would otherwise arrive as one long run-on line.
 */
export function plaintextAsMarkdown(text: string): string {
	return text.replace(/[\\`*_{}[\]()#+\-.!|<>~]/g, (c) => `\\${c}`).replace(/\n/g, "\n\n");
}

function fenced(value: string, language: string | undefined): string {
	// A fence has to be longer than the longest run of backticks inside it, or a
	// signature containing a markdown fence closes ours early and the rest of the
	// hover renders as prose.
	const longest = Math.max(2, ...Array.from(value.matchAll(/`+/g), (m) => m[0].length));
	const fence = "`".repeat(longest + 1);
	return `${fence}${language ?? ""}\n${value}\n${fence}`;
}

function partOf(part: LspMarkupContent | LspMarkedString): IMarkdownString | null {
	if (typeof part === "string") {
		// A bare `MarkedString` is markdown by the spec's own definition.
		return part.trim() === "" ? null : { value: part, isTrusted: false };
	}
	if (!part || typeof part !== "object") return null;
	const value = part.value;
	if (typeof value !== "string" || value.trim() === "") return null;
	// `MarkedString` with a language is code; `MarkupContent` has a `kind`
	// instead. The two shapes are told apart by which field is present, because
	// a server may legally send either and they disagree about what `value` means.
	const language = (part as { language?: string }).language;
	if (typeof language === "string") return { value: fenced(value, language), isTrusted: false };
	const kind = (part as LspMarkupContent).kind;
	// `markdown` is what the client capabilities asked for. Anything else —
	// including a server that omits `kind`, which the spec allows — is plaintext.
	return { value: kind === "markdown" ? value : plaintextAsMarkdown(value), isTrusted: false };
}

/** LSP is 0-based in both axes; Monaco is 1-based in both. */
function toMonacoRange(range: LspRange): IRange {
	return {
		startLineNumber: range.start.line + 1,
		startColumn: range.start.character + 1,
		endLineNumber: range.end.line + 1,
		endColumn: range.end.character + 1,
	};
}

function isRange(value: unknown): value is LspRange {
	const r = value as LspRange | undefined;
	return (
		typeof r?.start?.line === "number" &&
		typeof r.start.character === "number" &&
		typeof r.end?.line === "number" &&
		typeof r.end.character === "number"
	);
}

/**
 * Normalise every shape `textDocument/hover` may answer with into Monaco's.
 *
 * 🗝 Returns `null` — not an empty hover — when there is nothing to show.
 * `getHover.js:51`'s `isValid` requires both a range and non-empty contents, so
 * an "empty" hover and no hover render identically; returning null makes the
 * distinction explicit at the one place that can still tell them apart, which is
 * where the log line goes.
 *
 * The range is optional in the protocol and Monaco falls back to the word under
 * the pointer, which is the right default — so an absent range is carried as
 * absent rather than invented.
 */
export function toMonacoHover(result: unknown): languages.Hover | null {
	const hover = result as LspHover | null | undefined;
	if (!hover || typeof hover !== "object") return null;
	const raw = hover.contents;
	if (raw === null || raw === undefined) return null;
	const parts = (Array.isArray(raw) ? raw : [raw]).map(partOf).filter((part): part is IMarkdownString => part !== null);
	if (parts.length === 0) return null;
	return isRange(hover.range) ? { contents: parts, range: toMonacoRange(hover.range) } : { contents: parts };
}
