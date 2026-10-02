import { DEFAULT_EDITOR_SETTINGS, type EditorSettings } from "../src/shared/editor-settings";
import type { FormatRequest, FormatResult } from "../src/main/format/formatters";
import { reindentDocument } from "../src/renderer/lib/editor/formatting/indent-engine";
import { indentProfileFor } from "../src/renderer/lib/editor/formatting/indent-profiles";

/**
 * The main process's formatting channel, faked for the browser gallery: the
 * gallery has no main process, so no gofmt can run - but the editor's half of
 * Format Document (what it sends, how it applies the answer as one undo step,
 * what it says when the tool refuses) is all browser-side, and that is what
 * `editor-formatting.spec.ts` drives.
 *
 * The fake "gofmt" is deterministic and visibly different from re-indenting:
 * it also puts a space before a line's opening brace (`int{` → `int {`), so a
 * spec can tell the TOOL ran from the engine's fallback having run instead.
 */
export function installFakeFormatBridge(options: { mode: "ok" | "fail"; formatOnSave: boolean }): void {
	const asked: FormatRequest[] = [];
	(globalThis as { __aoFormatAsked?: FormatRequest[] }).__aoFormatAsked = asked;
	const settings: EditorSettings = { ...DEFAULT_EDITOR_SETTINGS, formatOnSave: options.formatOnSave };
	const existing = (globalThis as { ao?: Record<string, unknown> }).ao ?? {};
	(globalThis as { ao?: Record<string, unknown> }).ao = {
		...existing,
		editorSettings: {
			get: async () => settings,
			set: async (next: EditorSettings) => next,
		},
		format: {
			indentStyle: async () => null,
			run: async (request: FormatRequest): Promise<FormatResult> => {
				asked.push(request);
				// As slow as a real tool, so "the buffer changed meanwhile" is reachable.
				await new Promise((resolve) => setTimeout(resolve, 150));
				if (options.mode === "fail") {
					return {
						ok: false,
						reason: "failed",
						formatter: "gofmt",
						message: "messy.go:9:1: expected '}', found 'EOF'",
					};
				}
				const profile = indentProfileFor(request.languageId, request.filePath);
				const spaced = request.text.replace(/([^\s{])\{$/gm, "$1 {");
				const text = profile ? reindentDocument(spaced, profile, { insertSpaces: false, indentSize: 4 }) : spaced;
				return { ok: true, text, formatter: "gofmt" };
			},
		},
	};
}
