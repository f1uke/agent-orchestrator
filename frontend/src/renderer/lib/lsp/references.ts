import { monaco } from "../monaco-setup";
import { toMonacoDefinitions } from "./definition-mapping";
import { languageServerName } from "./language-ids";
import type { LspClient } from "./lsp-client";
import { pathForFileUri } from "./lsp-uri";
import { ensurePreviewModels, modelUriForTargetUri, PREVIEW_FILE_LIMIT } from "./peek-sources";
import { runInLane, withTimeout } from "./request-lane";

/**
 * Find all references — the counterpart to ⌘click.
 *
 * Monaco already owns everything a reader sees: `editor.action.goToReferences`
 * (⇧F12) and `editor.action.referenceSearch.trigger` (the editor's context menu,
 * "Peek References"), the peek widget, its tree, its grouping by file, its
 * sorting, and its "No references found for 'x'" message at the cursor when the
 * answer is empty (`goToCommands.js:114`). All of it registered by the barrel
 * import. The only missing piece was a `ReferenceProvider`, and the preview
 * models the widget reads out of — see `peek-sources.ts`.
 *
 * ## What "a lot of hits" actually looks like, measured
 *
 * | | latency | answer |
 * |---|---|---|
 * | gopls, a package-local func | 8–51 ms | 3 hits in 1 file |
 * | gopls, an exported type | 8 ms | 22 hits in 5 files |
 * | gopls, `Service` in this repo's backend | **7 ms** | **164 hits in 35 files** |
 * | sourcekit-lsp, a private `let` | 57–88 ms | 1 hit in 1 file |
 * | sourcekit-lsp, `UserDefaultManagerProtocol` | **67 ms** | **69 hits in 32 files** |
 *
 * 🗝 So the REQUEST is never the expensive half, on either server, and it is not
 * gated on the index either — measured, the same hits come back before and after
 * `workspace/synchronize`, unlike `workspace/symbol`. What costs is preparing a
 * preview per file, which is why that is the only thing capped:
 *
 * - **every hit the server returned is returned**, because the count is the
 *   answer to the question that was asked, and a silently shortened list is a
 *   wrong answer rather than a smaller one;
 * - **previews are materialised for at most {@link PREVIEW_FILE_LIMIT} files**,
 *   in parallel, and the rows past that render as Monaco's own
 *   `File.swift:12:5` fallback rather than as an empty file;
 * - **what the cap left out is logged**, with the numbers, so a short preview
 *   list is a stated limit rather than a mystery.
 *
 * And it never blocks the editor: the whole thing runs inside
 * `provideReferences`, which Monaco cancels through `token` the moment the
 * cursor or the buffer moves.
 */

export type ReferencesDocument = {
	languageId: string;
	/** The model this pane is showing, so the provider answers per document. */
	modelUri: string;
	getClient: () => LspClient | null;
	getAbsolutePath: () => string | null;
	/** Slice 3's state, so a 0-hit answer can say WHY rather than look like a dead feature. */
	getState: () => string;
	/** Reads a file for the peek preview. Null for one that cannot be shown. */
	readFile: (absolutePath: string) => Promise<string | null>;
	/** Shown at the cursor — this is an EXPLICIT ask, so a refusal gets an answer. */
	onUnavailable?: (reason: string) => void;
};

type LspLocation = { uri?: string; range?: unknown };

/**
 * 🗝 `undefined`, never a throw and never an empty array.
 *
 * Monaco's `getReferencesAtPosition` routes a rejected provider to
 * `onUnexpectedExternalError`, the same channel `suggest.js` and `getHover.js`
 * use — a stack trace with no reason attached. And an EMPTY array is a real
 * answer: it makes Monaco print "No references found for 'x'" at the cursor,
 * which is exactly the wrong thing to say about a server that has not started.
 */
const NO_ANSWER = undefined;

const documentsByLanguage = new Map<string, Map<string, ReferencesDocument>>();
const registered = new Map<string, { documents: Map<string, ReferencesDocument>; dispose: () => void }>();
/** Which provider call is the current one, per document. Per FEATURE — see `request-lane.ts`. */
const generations = new Map<string, number>();

function documentsFor(languageId: string): Map<string, ReferencesDocument> {
	let documents = documentsByLanguage.get(languageId);
	if (!documents) {
		documents = new Map();
		documentsByLanguage.set(languageId, documents);
	}
	return documents;
}

function provider(documents: Map<string, ReferencesDocument>): monaco.languages.ReferenceProvider {
	return {
		async provideReferences(model, position, context, token) {
			const source = documents.get(model.uri.toString());
			if (!source) return NO_ANSWER;
			// Every route into this provider is a deliberate gesture — ⇧F12, or a
			// context-menu item somebody chose — so a refusal always says why, at
			// the cursor and in the log. That is the opposite of hover's rule, and
			// for the same reason: what the reader did.
			const refuse = (reason: string): undefined => {
				// 🗝 Deferred by a turn, and that is not a nicety. Monaco shows its OWN
				// message for an answer it reads as empty — `goToCommands.js:114`,
				// "No references found for 'x'" — from the `.then` that follows this
				// provider settling. Said at the cursor now, ours would be overwritten
				// a moment later by a sentence that is false: the server did not fail
				// to find references, it does not do reference search at all. This is
				// the one place the two messages compete, and the true one has to win.
				if (source.onUnavailable) setTimeout(() => source.onUnavailable?.(reason), 0);
				console.warn(`[lsp] ${source.languageId} references unavailable: ${reason}`);
				return NO_ANSWER;
			};

			const client = source.getClient();
			const absolute = source.getAbsolutePath();
			if (!client || !absolute) return refuse(`the language server is ${source.getState()}`);
			if (!client.features().references) {
				return refuse(`${languageServerName(source.languageId)} offers no reference search for this file`);
			}

			const generation = (generations.get(source.modelUri) ?? 0) + 1;
			generations.set(source.modelUri, generation);
			const stale = () => token.isCancellationRequested || generations.get(source.modelUri) !== generation;

			const startedAt = performance.now();
			let result: unknown;
			try {
				const outcome = await runInLane(source.modelUri, stale, () =>
					withTimeout(() =>
						client.request<unknown>("textDocument/references", {
							// 🗝 `client.documentUri`, never `fileUriForPath`.
							textDocument: { uri: client.documentUri(absolute) },
							position: { line: position.lineNumber - 1, character: position.column - 1 },
							context: { includeDeclaration: context.includeDeclaration !== false },
						}),
					),
				);
				if (!outcome.ok) return NO_ANSWER;
				result = outcome.value;
			} catch (err) {
				return refuse(err instanceof Error ? err.message : "the request failed");
			}
			if (stale()) return NO_ANSWER;

			// `toMonacoDefinitions` is exactly the right shape here: a `Location[]`
			// is what both answer with, and reusing it means references and ⌘click
			// cannot drift apart on the 0-based/1-based arithmetic.
			const links = toMonacoDefinitions(result, model.uri, (uri) => modelUriForTargetUri(uri));
			const elapsed = Math.round(performance.now() - startedAt);
			if (links.length === 0) {
				// Monaco says "No references found for 'x'" at the cursor by itself.
				// This line is for the machine, and the STATE is part of the sentence:
				// nothing at all from a server that is still starting is a wait, and
				// nothing from a ready one is an answer.
				console.warn(
					`[lsp] ${source.languageId} references → 0 locations at ${absolute}:${position.lineNumber}` +
						` (${elapsed}ms, server ${source.getState()})`,
				);
				return [];
			}

			// One read per FILE, not one per hit: 164 hits across 35 files is 35
			// reads, and asking for the same file 164 times is how a 7 ms request
			// turns into a frozen editor.
			const files = new Set<string>();
			for (const item of (Array.isArray(result) ? result : [result]) as LspLocation[]) {
				if (item && typeof item.uri === "string") files.add(pathForFileUri(item.uri));
			}
			const preview = await ensurePreviewModels([...files], source.readFile);
			if (stale()) return NO_ANSWER;
			if (preview.missing > 0) {
				// Said out loud. Monaco renders an unresolved row as `File.swift:12:5`
				// rather than as source, which is a legible degradation only if
				// somebody can find out WHY it happened.
				console.warn(
					`[lsp] ${source.languageId} references: ${links.length} hits in ${files.size} files;` +
						` ${preview.missing} of those files have no preview` +
						(files.size > PREVIEW_FILE_LIMIT ? ` (the cap is ${PREVIEW_FILE_LIMIT})` : " (unreadable)"),
				);
			}
			return links;
		},
	};
}

export function registerReferences(document: ReferencesDocument): monaco.IDisposable {
	const { languageId, modelUri } = document;
	const documents = documentsFor(languageId);
	documents.set(modelUri, document);
	if (!registered.has(languageId)) {
		const disposable = monaco.languages.registerReferenceProvider(languageId, provider(documents));
		registered.set(languageId, { documents, dispose: () => disposable.dispose() });
	}

	let released = false;
	return {
		dispose: () => {
			if (released) return;
			released = true;
			documents.delete(modelUri);
			generations.delete(modelUri);
			if (documents.size > 0) return;
			documentsByLanguage.delete(languageId);
			const owner = registered.get(languageId);
			if (!owner) return;
			registered.delete(languageId);
			owner.dispose();
		},
	};
}
