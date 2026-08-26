import { monaco } from "../monaco-setup";
import type { WorkspaceFileOpen } from "../open-workspace-file";
import type { LspClient } from "./lsp-client";
import { toMonacoDefinitions } from "./definition-mapping";
import { openTargetForUri } from "./lsp-uri";

/**
 * ⌘click, in two halves that are BOTH required.
 *
 * 🗝 A DefinitionProvider alone gives you a ⌘click that resolves correctly and
 * then does not move. monaco-editor 0.56's own `editor.api.d.ts:1176` says it
 * out loud: "If no handler is registered the default behavior is to do nothing
 * for models other than the currently attached one." So the provider answers the
 * question, and `registerEditorOpener` acts on the answer by routing it through
 * this app's single file-open seam.
 *
 * Prove this by watching the editor MOVE. The absence of an error proves
 * nothing here - silence is the failure mode, not the success case.
 */

export type LspNavigationInput = {
	languageId: string;
	getClient: () => LspClient | null;
	/** So a 0-hit answer can say WHY, rather than being indistinguishable from broken. */
	getState: () => string;
	getWorkspaceRoot: () => string | undefined;
	getAbsolutePath: (modelUri: monaco.Uri) => string | null;
	openFile: (file: WorkspaceFileOpen) => void;
};

/**
 * Monaco's provider registry is GLOBAL, so registration is refcounted per
 * language. Registering per editor would ask the server the same question twice
 * on one click and show a duplicated peek list.
 */
const registered = new Map<string, { count: number; dispose: () => void }>();

export function registerLspNavigation(input: LspNavigationInput): monaco.IDisposable {
	const { languageId } = input;
	const existing = registered.get(languageId);
	if (existing) {
		existing.count++;
		return { dispose: () => release(languageId) };
	}

	// The original `file:` URI of a resolved target, stashed between the provider
	// answering and the opener firing. Monaco hands the opener a MODEL uri, but
	// the path and containment decision needs the server's own answer, so it is
	// carried across rather than re-derived from the `ao-file:` URI's shape.
	const targets = new Map<string, string>();

	const definitions = monaco.languages.registerDefinitionProvider(languageId, {
		async provideDefinition(model, position) {
			const client = input.getClient();
			const absolute = input.getAbsolutePath(model.uri);
			if (!client || !absolute) return [];
			const startedAt = performance.now();
			let result: unknown;
			try {
				result = await client.request("textDocument/definition", {
					// 🗝 `client.documentUri`, never `fileUriForPath`. On Swift the two
					// differ, and using the wrong one returns 0 hits with no error.
					textDocument: { uri: client.documentUri(absolute) },
					position: { line: position.lineNumber - 1, character: position.column - 1 },
				});
			} catch (err) {
				console.warn(`[lsp] ${languageId} definition failed at ${absolute}:${position.lineNumber}`, err);
				return [];
			}
			const links = toMonacoDefinitions(result, model.uri, (uri) => {
				const modelUri = monaco.Uri.parse(uri.replace(/^file:\/\//, "ao-file://"));
				targets.set(modelUri.toString(), uri);
				return modelUri;
			});
			if (links.length === 0) {
				// Up, answering, and returning nothing. Logged so it is
				// DISTINGUISHABLE in the console from a server that is broken - which
				// is the whole difference this slice is trying to make visible.
				//
				// 🗝 And the state is part of the sentence. Measured on sourcekit-lsp
				// against a real iOS project: before the index has loaded, one of four
				// ⌘click targets returns nothing and the rest take 1.7-2.5 s, while
				// after it all four land in 59-67 ms. "0 locations" during that window
				// is the index, not the code, and saying so is the difference between
				// a known wait and an apparently broken feature.
				const state = input.getState();
				console.warn(
					`[lsp] ${languageId} definition → 0 locations at ${absolute}:${position.lineNumber}:${position.column}` +
						` (${Math.round(performance.now() - startedAt)}ms, server ${state})` +
						(state === "ready" ? "" : " - the index is still loading, so this is expected"),
				);
			}
			return links;
		},
	});

	const opener = monaco.editor.registerEditorOpener({
		openCodeEditor(_source, resource, selectionOrPosition) {
			const fileUri = targets.get(resource.toString());
			const workspaceRoot = input.getWorkspaceRoot();
			// Not ours: let Monaco (or another opener) have it rather than swallowing
			// the navigation by claiming to have handled it.
			if (!fileUri || !workspaceRoot) return false;
			const at = selectionOrPosition as Partial<monaco.IRange> & Partial<monaco.IPosition>;
			input.openFile(
				openTargetForUri({
					uri: fileUri,
					workspaceRoot,
					line: at?.startLineNumber ?? at?.lineNumber,
					column: at?.startColumn ?? at?.column,
				}),
			);
			// `true` means handled. Returning false here is the silent no-op: the
			// definition resolves and the editor simply never moves.
			return true;
		},
	});

	registered.set(languageId, {
		count: 1,
		dispose: () => {
			definitions.dispose();
			opener.dispose();
			targets.clear();
		},
	});
	return { dispose: () => release(languageId) };
}

function release(languageId: string): void {
	const entry = registered.get(languageId);
	if (!entry) return;
	entry.count--;
	if (entry.count > 0) return;
	registered.delete(languageId);
	entry.dispose();
}
