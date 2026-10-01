import type { FormatRequest, FormatResult } from "../../../../main/format/formatters";
import { formatterFor } from "../../../../shared/formatter-languages";
import { type IndentUnit, reindentDocument } from "./indent-engine";
import { indentProfileFor } from "./indent-profiles";

/**
 * Format Document, as a decision about WHO formats - kept apart from Monaco so
 * every branch of it can be tested without an editor.
 *
 * In order, the first that answers wins:
 *
 * 1. The language server AO already runs, when it is attached and said it
 *    formats (gopls, sourcekit-lsp). It already holds the buffer.
 * 2. The standard tool on this Mac, run by the main process: gofmt,
 *    swift-format, or the project's own Prettier when the project configures it.
 * 3. Re-indenting, for a language whose structure is its brackets.
 *
 * A tool that RAN and refused (a syntax error) ends the search: the next tool
 * would refuse the same file, and quietly re-indenting instead would pass off
 * a half-measure as a format.
 */

export type LspTextEdit = {
	range: { start: { line: number; character: number }; end: { line: number; character: number } };
	newText: string;
};

export type LspFormatter = {
	/** "gopls" / "sourcekit-lsp", for messages. */
	name: string;
	/** Exactly what the server holds. Formatting is only asked for while it IS the buffer. */
	serverText(): string;
	request(options: { tabSize: number; insertSpaces: boolean }): Promise<LspTextEdit[] | null>;
};

export type FormatInput = {
	/** Monaco language id. */
	languageId: string;
	/** Human name for messages ("Python"). */
	languageName: string;
	/** The path as the pane knows it - decides whether a TypeScript file may hold JSX. */
	path: string;
	absolutePath?: string;
	workspaceRoot?: string;
	text: string;
	unit: IndentUnit & { tabSize: number };
	lsp: LspFormatter | null;
	runTool: ((request: FormatRequest) => Promise<FormatResult>) | null;
	lspTimeoutMs?: number;
};

export type FormatOutcome =
	/** `note` says why it was only re-indented. */
	| { kind: "formatted"; text: string; formatter: string; note?: string }
	| { kind: "failed"; formatter: string; message: string }
	| { kind: "unsupported"; message: string };

/** sourcekit-lsp's first request in a file waits on its type-check (measured ~1.9 s); this is well past that. */
const DEFAULT_LSP_TIMEOUT_MS = 6000;

export async function formatText(input: FormatInput): Promise<FormatOutcome> {
	const { text, unit } = input;
	// LSP's `tabSize` is the width of one level - spaces when indenting with
	// spaces, a tab's width otherwise.
	const width = unit.insertSpaces ? unit.indentSize : unit.tabSize;
	let lspFailure: string | null = null;

	if (input.lsp && input.lsp.serverText() === text) {
		try {
			const edits = await withTimeout(
				input.lsp.request({ tabSize: width, insertSpaces: unit.insertSpaces }),
				input.lspTimeoutMs ?? DEFAULT_LSP_TIMEOUT_MS,
				`${input.lsp.name} did not answer in time`,
			);
			return { kind: "formatted", text: applyLspEdits(text, edits ?? []), formatter: input.lsp.name };
		} catch (error) {
			lspFailure = error instanceof Error ? error.message : String(error);
		}
	}

	let unavailable: string | undefined;
	const tool = formatterFor(input.languageId);
	if (tool && !(input.runTool && input.absolutePath)) unavailable = `${tool} could not be run on this file here.`;
	if (tool && input.runTool && input.absolutePath) {
		const result = await input.runTool({
			languageId: input.languageId,
			filePath: input.absolutePath,
			workspaceRoot: input.workspaceRoot,
			text,
			insertSpaces: unit.insertSpaces,
			tabSize: width,
		});
		if (result.ok) return { kind: "formatted", text: result.text, formatter: result.formatter };
		if (result.reason === "failed") {
			return { kind: "failed", formatter: result.formatter ?? tool, message: result.message };
		}
		unavailable = result.message;
	}

	if (lspFailure !== null && input.lsp) return { kind: "failed", formatter: input.lsp.name, message: lspFailure };

	const profile = indentProfileFor(input.languageId, input.path);
	if (profile) {
		return {
			kind: "formatted",
			text: reindentDocument(text, profile, unit),
			formatter: "re-indent",
			note: unavailable ?? `There is no formatter for ${input.languageName} files.`,
		};
	}
	return { kind: "unsupported", message: unavailable ?? `There is no formatter for ${input.languageName} files.` };
}

/**
 * Apply an LSP formatting answer to the text it was computed against.
 * Positions are UTF-16, which is what a JS string index already is.
 */
export function applyLspEdits(text: string, edits: readonly LspTextEdit[]): string {
	if (edits.length === 0) return text;
	const starts = [0];
	for (let i = 0; i < text.length; i++) if (text.charCodeAt(i) === 10) starts.push(i + 1);
	const offset = (p: { line: number; character: number }) => {
		if (p.line >= starts.length) return text.length;
		const lineStart = starts[p.line];
		const lineEnd = p.line + 1 < starts.length ? starts[p.line + 1] - 1 : text.length;
		// A CRLF line's `\r` is part of the line break, never a column.
		const contentEnd = lineEnd > lineStart && text[lineEnd - 1] === "\r" ? lineEnd - 1 : lineEnd;
		return Math.min(lineStart + Math.max(0, p.character), contentEnd);
	};
	const resolved = edits
		.map((e) => ({ start: offset(e.range.start), end: offset(e.range.end), text: e.newText }))
		.sort((a, b) => b.start - a.start || b.end - a.end);
	let out = text;
	for (const edit of resolved) out = out.slice(0, edit.start) + edit.text + out.slice(edit.end);
	return out;
}

function withTimeout<T>(promise: Promise<T>, ms: number, message: string): Promise<T> {
	return new Promise((resolve, reject) => {
		const timer = setTimeout(() => reject(new Error(message)), ms);
		promise.then(
			(value) => {
				clearTimeout(timer);
				resolve(value);
			},
			(error: unknown) => {
				clearTimeout(timer);
				reject(error);
			},
		);
	});
}
