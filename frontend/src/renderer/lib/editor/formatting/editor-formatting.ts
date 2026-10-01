import type { FormatRequest, FormatResult } from "../../../../main/format/formatters";
import type { EditorSettings } from "../../../../shared/editor-settings";
import { matchesShortcut } from "../../../../shared/editor-shortcuts";
import { monaco } from "../../monaco-setup";
import { type FormatOutcome, formatText, type LspFormatter } from "./format-document";
import { computeReindent, type IndentChange, type IndentUnit } from "./indent-engine";
import { indentProfileFor } from "./indent-profiles";
import { minimalEdits } from "./text-diff";

/**
 * The editor's formatting, wired into one Monaco editor: indentation as you
 * type, ⌃I re-indent, and ⌃⇧I format document.
 *
 * Everything here goes through the MODEL, never around it, so each operation
 * is undoable - and each is exactly one undo step:
 *
 * - Return and a closing bracket fix the new line's indentation INSIDE the
 *   typing's own undo step (Monaco opens one for Return and does not close it,
 *   so an edit made in `onDidType` joins it): ⌘Z takes back the keystroke and
 *   its indentation together.
 * - A paste's re-indent is its own step after the paste's, exactly as VS
 *   Code's auto-indent-on-paste does: the paste command closes its step, and ⌘Z
 *   once gives back the text as it was pasted.
 * - Re-indent and format are bracketed by undo stops.
 *
 * Keys are matched in `onKeyDown` rather than registered with Monaco's
 * keybinding service, for the same reason ⌘S is: Changes mode's editable side
 * is a plain ICodeEditor with no `addAction`, and a shortcut the user re-binds
 * in Settings has to take effect without rebuilding the editor. Tab is never
 * touched.
 */

export type FormatReport = {
	/** Whether the buffer changed. */
	applied: boolean;
	/** What was said at the caret, if anything. */
	message?: string;
};

export type EditorFormattingOptions = {
	editor: monaco.editor.ICodeEditor;
	/** The pane's path (decides JSX for TypeScript), and the absolute one a tool needs. */
	getPath(): string;
	getAbsolutePath(): string | undefined;
	getWorkspaceRoot(): string | undefined;
	getSettings(): EditorSettings;
	isReadOnly(): boolean;
	/** The attached language server, when it can format right now. */
	getLsp(): LspFormatter | null;
	/** The main process's formatter runner; null where there is none (browser preview). */
	runTool: ((request: FormatRequest) => Promise<FormatResult>) | null;
	showMessage(text: string): void;
};

export type EditorFormatting = {
	reindentSelection(): void;
	formatDocument(reason: "command" | "save"): Promise<FormatReport>;
	/** Re-read the settings and the model's language (autoIndent depends on both). */
	refresh(): void;
	dispose(): void;
};

/** How long Format Document runs before the caret says it is still working. */
const SLOW_FORMAT_MS = 400;

export function attachEditorFormatting(options: EditorFormattingOptions): EditorFormatting {
	const { editor } = options;
	const disposables: monaco.IDisposable[] = [];
	let formatting: Promise<FormatReport> | null = null;

	const model = () => editor.getModel();
	const languageId = () => model()?.getLanguageId() ?? "plaintext";
	const profile = () => indentProfileFor(languageId(), options.getPath());

	const unitOf = (m: monaco.editor.ITextModel): IndentUnit & { tabSize: number } => {
		const o = m.getOptions();
		return { insertSpaces: o.insertSpaces, indentSize: o.indentSize, tabSize: o.tabSize };
	};

	/**
	 * Monaco's own indentation, under ours. With a profile: brackets and
	 * on-enter rules (`advanced`) - so Return between `{}` still splits the pair
	 * and a doc comment still continues - but NOT its regex rules or its
	 * paste re-indent, which would fight the engine. Without one (Python, YAML):
	 * Monaco's full rules, which are all there is. Off: keep the line's indent.
	 */
	const applyAutoIndent = () => {
		const on = options.getSettings().indentOnType;
		editor.updateOptions({ autoIndent: !on ? "keep" : profile() ? "advanced" : "full" });
	};

	/** Replace each changed line's leading whitespace, mapping every caret through it. */
	const applyIndentChanges = (changes: IndentChange[], source: string, ownUndoStep: boolean) => {
		const m = model();
		if (!m || changes.length === 0) return;
		const byLine = new Map(changes.map((c) => [c.line + 1, c]));
		const map = (lineNumber: number, column: number) => {
			const change = byLine.get(lineNumber);
			if (!change) return column;
			const oldEnd = change.previous.length + 1;
			// A caret inside the old indentation lands where the code now starts.
			return column <= oldEnd ? change.indent.length + 1 : column + change.indent.length - change.previous.length;
		};
		const selections = (editor.getSelections() ?? []).map(
			(s) =>
				new monaco.Selection(
					s.selectionStartLineNumber,
					map(s.selectionStartLineNumber, s.selectionStartColumn),
					s.positionLineNumber,
					map(s.positionLineNumber, s.positionColumn),
				),
		);
		const edits = changes.map((c) => ({
			range: new monaco.Range(c.line + 1, 1, c.line + 1, c.previous.length + 1),
			text: c.indent,
		}));
		if (ownUndoStep) editor.pushUndoStop();
		editor.executeEdits(source, edits, selections);
		if (ownUndoStep) editor.pushUndoStop();
	};

	const reindentRange = (from: number, to: number, indentBlank: Set<number>): IndentChange[] => {
		const m = model();
		const p = profile();
		if (!m || !p) return [];
		return computeReindent({ lines: m.getLinesContent(), profile: p, unit: unitOf(m), from, to, indentBlank });
	};

	// --- as you type ---------------------------------------------------------

	const onTyped = (text: string) => {
		if (!options.getSettings().indentOnType || options.isReadOnly()) return;
		const m = model();
		if (!m || !profile()) return;
		const carets = (editor.getSelections() ?? []).filter((s) => s.isEmpty());
		if (carets.length === 0) return;
		const changes: IndentChange[] = [];
		const seen = new Set<number>();
		const add = (list: IndentChange[]) => {
			for (const change of list) {
				if (seen.has(change.line)) continue;
				seen.add(change.line);
				changes.push(change);
			}
		};

		if (text === "\n" || text === "\r\n") {
			for (const caret of carets) {
				const line = caret.positionLineNumber - 1;
				const content = m.getLineContent(line + 1);
				const blank = content.trim() === "";
				add(reindentRange(line, line, blank ? new Set([line]) : new Set()));
				// Return between `{` and `}`: Monaco has already moved the `}` to a
				// line of its own below the caret. It is part of this keystroke.
				const above = line > 0 ? m.getLineContent(line).trimEnd() : "";
				const below = line + 2 <= m.getLineCount() ? m.getLineContent(line + 2).trimStart() : "";
				if (blank && /[{[(]$/.test(above) && /^[}\])]/.test(below)) add(reindentRange(line + 1, line + 1, new Set()));
			}
		} else if (text === "}" || text === "]" || text === ")") {
			for (const caret of carets) {
				const line = caret.positionLineNumber - 1;
				const before = m.getLineContent(line + 1).slice(0, caret.positionColumn - 2);
				// Only a closer that is the first thing on its line moves its line.
				if (before.trim() === "") add(reindentRange(line, line, new Set()));
			}
		} else if (text === ":") {
			for (const caret of carets) {
				const line = caret.positionLineNumber - 1;
				const content = m.getLineContent(line + 1);
				// Xcode's electric `:` - a `case` label finds its level as it is finished.
				if (
					caret.positionColumn - 1 === content.trimEnd().length &&
					/^\s*(case\b.*|default\s*|@unknown\s+default\s*):$/.test(content)
				) {
					add(reindentRange(line, line, new Set()));
				}
			}
		} else {
			return;
		}
		// No undo stop of its own: this joins the keystroke's step.
		applyIndentChanges(changes, "ao.indentOnType", false);
	};

	const onPasted = (event: monaco.editor.IPasteEvent) => {
		if (!options.getSettings().indentOnType || options.isReadOnly()) return;
		const m = model();
		if (!m || !profile()) return;
		const { startLineNumber, startColumn, endLineNumber } = event.range;
		const firstLine = m.getLineContent(startLineNumber);
		// Pasted after code on its first line: that line is not the paste's to move.
		const afterCode = firstLine.slice(0, startColumn - 1).trim() !== "";
		const from = afterCode ? startLineNumber : startLineNumber - 1;
		const to = endLineNumber - 1;
		if (from > to) return;
		applyIndentChanges(reindentRange(from, to, new Set()), "ao.indentOnPaste", true);
	};

	// --- ⌃I ------------------------------------------------------------------

	const reindentSelection = () => {
		const m = model();
		if (!m) return;
		if (options.isReadOnly()) {
			options.showMessage("This file is read-only.");
			return;
		}
		if (!profile()) {
			options.showMessage(`Re-indent isn't available for ${languageName(languageId())} files.`);
			return;
		}
		const changes: IndentChange[] = [];
		const seen = new Set<number>();
		for (const s of editor.getSelections() ?? []) {
			let last = s.endLineNumber;
			// A selection ending at the very start of a line does not include it.
			if (last > s.startLineNumber && s.endColumn === 1) last--;
			const blank = s.isEmpty() && m.getLineContent(s.startLineNumber).trim() === "";
			const indentBlank = blank ? new Set([s.startLineNumber - 1]) : new Set<number>();
			for (const change of reindentRange(s.startLineNumber - 1, last - 1, indentBlank)) {
				if (seen.has(change.line)) continue;
				seen.add(change.line);
				changes.push(change);
			}
		}
		applyIndentChanges(changes, "ao.reindent", true);
	};

	// --- ⌃⇧I -----------------------------------------------------------------

	const runFormat = async (reason: "command" | "save"): Promise<FormatReport> => {
		const m = model();
		if (!m) return { applied: false };
		if (options.isReadOnly()) {
			const message = "This file is read-only.";
			if (reason === "command") options.showMessage(message);
			return { applied: false, message };
		}
		const version = m.getVersionId();
		const text = m.getValue();
		const slow = reason === "command" ? setTimeout(() => options.showMessage("Formatting…"), SLOW_FORMAT_MS) : null;
		let outcome: FormatOutcome;
		try {
			outcome = await formatText({
				languageId: languageId(),
				languageName: languageName(languageId()),
				path: options.getPath(),
				absolutePath: options.getAbsolutePath(),
				workspaceRoot: options.getWorkspaceRoot(),
				text,
				unit: unitOf(m),
				lsp: options.getLsp(),
				runTool: options.runTool,
			});
		} catch (error) {
			// Nothing below formatText is supposed to throw; if something does, the
			// buffer is still untouched and the reader is told so.
			outcome = {
				kind: "failed",
				formatter: "formatter",
				message: error instanceof Error ? error.message : String(error),
			};
		} finally {
			if (slow) clearTimeout(slow);
		}

		const say = (message: string, always = false): FormatReport => {
			if (reason === "command" || always) options.showMessage(message);
			return { applied: false, message };
		};
		if (m.isDisposed() || editor.getModel() !== m) return { applied: false };
		if (outcome.kind === "unsupported") return say(`${outcome.message} Nothing was changed.`);
		if (outcome.kind === "failed") {
			const prefix = reason === "save" ? "Saved without formatting" : `Couldn't format with ${outcome.formatter}`;
			return say(`${prefix}: ${outcome.message}`, true);
		}
		// 🗝 The text was formatted as it was when this started. If the reader has
		// typed since, applying it would undo their typing - so it is dropped.
		if (m.getVersionId() !== version) {
			return say("The file changed while it was being formatted, so nothing was applied.", true);
		}
		const formatted = toModelEol(outcome.text, m.getEOL());
		const edits = minimalEdits(text, formatted);
		if (edits.length === 0) {
			const message =
				outcome.formatter === "re-indent"
					? `Already indented. ${outcome.note ?? ""}`.trim()
					: `Already formatted (${outcome.formatter}).`;
			return say(message);
		}
		editor.pushUndoStop();
		editor.executeEdits(
			"ao.format",
			edits.map((edit) => {
				const start = m.getPositionAt(edit.start);
				const end = m.getPositionAt(edit.end);
				return { range: new monaco.Range(start.lineNumber, start.column, end.lineNumber, end.column), text: edit.text };
			}),
		);
		editor.pushUndoStop();
		if (outcome.formatter === "re-indent") {
			const message = `Re-indented. ${outcome.note ?? ""}`.trim();
			if (reason === "command") options.showMessage(message);
			return { applied: true, message };
		}
		return { applied: true };
	};

	const formatDocument = (reason: "command" | "save"): Promise<FormatReport> => {
		// One at a time: a second ⌃⇧I while the first is running waits for it
		// rather than formatting the same text twice.
		formatting ??= runFormat(reason).finally(() => {
			formatting = null;
		});
		return formatting;
	};

	// 🗝 `onDidType` is not in Monaco's public `.d.ts`, though every editor this
	// app creates - the standalone one and both sides of the diff editor - is a
	// CodeEditorWidget that fires it: synchronously, after the keystroke's edit
	// and before its undo step closes, which is the whole trick above.
	const typed = (editor as monaco.editor.ICodeEditor & { onDidType?: monaco.IEvent<string> }).onDidType;
	if (typed) disposables.push(typed.call(editor, onTyped));
	disposables.push(
		editor.onDidPaste(onPasted),
		editor.onKeyDown((event) => {
			const key = event.browserEvent;
			if (key.isComposing) return;
			const settings = options.getSettings();
			if (matchesShortcut(key, settings.reindentShortcut)) {
				event.preventDefault();
				event.stopPropagation();
				reindentSelection();
			} else if (matchesShortcut(key, settings.formatShortcut)) {
				event.preventDefault();
				event.stopPropagation();
				void formatDocument("command");
			}
		}),
		editor.onDidChangeModel(applyAutoIndent),
		editor.onDidChangeModelLanguage(applyAutoIndent),
	);
	applyAutoIndent();

	return {
		reindentSelection,
		formatDocument,
		refresh: applyAutoIndent,
		dispose() {
			for (const d of disposables) d.dispose();
		},
	};
}

function toModelEol(text: string, eol: string): string {
	const lf = text.replace(/\r\n?/g, "\n");
	return eol === "\n" ? lf : lf.replace(/\n/g, eol);
}

function languageName(id: string): string {
	const known = monaco.languages.getLanguages().find((l) => l.id === id);
	return known?.aliases?.[0] ?? id;
}
