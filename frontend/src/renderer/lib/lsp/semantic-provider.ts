import { monaco } from "../monaco-setup";
import type { LspClient } from "./lsp-client";
import {
	AO_SEMANTIC_LEGEND,
	type GrammarLines,
	paintSemanticTokens,
	type SemanticTokensLegend,
	type SemanticTokensResult,
} from "./semantic-tokens";

/**
 * The Monaco half of semantic tokens: one provider per LANGUAGE, one document
 * source per open pane.
 *
 * 🗝 Monaco's provider registry is GLOBAL and it asks the provider about EVERY
 * model of that language, not about the pane that registered. Two Swift files
 * open in two panes is the ordinary case, so the sources are keyed by model URI
 * - registering per pane and answering from a closed-over path would paint one
 * file with the other's colours.
 *
 * Only the pane's OWN model is answered for. In Changes mode Monaco holds a
 * second model of the same language for the original side, and that side is a
 * different revision from the one the server was given - so it keeps the
 * grammar's colouring rather than being painted from the wrong file's answer.
 *
 * 🗝 A throw means "not right now", and that is load-bearing.
 * `documentSemanticTokens.js` keeps the tokens already applied when a request
 * REJECTS and clears them when it resolves to null, and `SparseTokensStore`
 * shifts what is applied across each edit. So every "cannot answer" path here
 * throws: nothing is ever blanked while the server is starting, while the buffer
 * is dirty, or after the server has gone away. Monaco treats an error whose
 * message contains "busy" as expected and does not log it, which is exactly what
 * these are.
 */

export type SemanticDocument = {
	languageId: string;
	/** The model this pane is showing, so the provider answers per document. */
	modelUri: string;
	getClient: () => LspClient | null;
	getAbsolutePath: () => string | null;
	/**
	 * The text the SERVER has, which is the last saved text - this app sends
	 * `didOpen`/`didClose` and no `didChange`. Tokens computed against anything
	 * else land on the wrong columns, so a mismatch is a reason to wait, not to
	 * answer.
	 */
	getServerText: () => string;
	/** So a 0-token answer can say WHY rather than look like a dead feature. */
	getState: () => string;
};

export type SemanticRegistration = monaco.IDisposable & {
	/** Ask Monaco to come back - the server was not attached when it last did. */
	refresh(): void;
};

type LanguageEntry = {
	documents: Map<string, SemanticDocument>;
	emitter: monaco.Emitter<void>;
	dispose: () => void;
};

const registered = new Map<string, LanguageEntry>();

/** Files above this many lines skip the grammar pass. See `grammarFor`. */
const GRAMMAR_LINE_LIMIT = 20_000;

/** A rejection Monaco understands as "temporarily unavailable", and does not log. */
function notNow(reason: string): Error {
	return new Error(`semantic tokens busy: ${reason}`);
}

/**
 * What the grammar made of the file, so the semantic layer can fill the gaps it
 * left rather than paint over what it knew.
 *
 * `monaco.editor.tokenize` is public and, under `@shikijs/monaco`, answers with
 * shiki's own tokens reverse-mapped to a scope name. Measured: 53 ms at 120
 * lines, 185 ms at 1902 lines, paid once per answered request.
 */
function grammarFor(model: monaco.editor.ITextModel, text: string): GrammarLines {
	if (model.getLineCount() > GRAMMAR_LINE_LIMIT) {
		// The pass is linear in the file, and past this size it would be a visible
		// hitch for a file nobody reads top to bottom. Declaration sites keep the
		// grammar's own colouring; references are unaffected.
		console.warn(
			`[lsp] ${model.getLineCount()} lines: skipping the grammar pass, declaration colours stay as the grammar left them`,
		);
		return [];
	}
	return monaco.editor
		.tokenize(text, model.getLanguageId())
		.map((line) => line.map((t) => ({ offset: t.offset, scope: t.type })));
}

function provider(entry: LanguageEntry): monaco.languages.DocumentSemanticTokensProvider {
	return {
		onDidChange: entry.emitter.event,
		getLegend(): SemanticTokensLegend {
			return { tokenTypes: [...AO_SEMANTIC_LEGEND.tokenTypes], tokenModifiers: [...AO_SEMANTIC_LEGEND.tokenModifiers] };
		},
		async provideDocumentSemanticTokens(model) {
			const source = entry.documents.get(model.uri.toString());
			if (!source) throw notNow("no pane owns this model");
			const client = source.getClient();
			const absolute = source.getAbsolutePath();
			if (!client || !absolute) throw notNow(`server ${source.getState()}`);
			const serverText = source.getServerText();
			// 🗝 The server's copy is the SAVED text. Answering while the buffer is
			// dirty would place last-saved columns on edited lines; throwing leaves
			// what is on screen, which Monaco keeps shifting across the edits.
			if (model.getValue() !== serverText) throw notNow("buffer has unsaved edits");

			const startedAt = performance.now();
			let result: SemanticTokensResult | null;
			try {
				result = await client.request<SemanticTokensResult | null>("textDocument/semanticTokens/full", {
					// The server's OWN address for this file. On Swift that is not its
					// real path, and the wrong one is answered with silence.
					textDocument: { uri: client.documentUri(absolute) },
				});
			} catch (err) {
				console.warn(`[lsp] ${source.languageId} semantic tokens failed at ${absolute}`, err);
				throw notNow("the request failed");
			}
			const legend = client.semanticTokensLegend();
			if (!legend) throw notNow("the server advertised no legend");

			const painted = paintSemanticTokens({
				result,
				legend,
				grammar: grammarFor(model, serverText),
				lineText: (line) => (line < model.getLineCount() ? model.getLineContent(line + 1) : ""),
			});
			if (painted.painted === 0) {
				// Up, answering, and painting nothing. Logged so it is DISTINGUISHABLE
				// from a feature that was never wired - which is this stack's
				// characteristic failure. `full` blocks until the file is type-checked
				// (measured 1.4-1.9 s cold), so this really should not happen once the
				// server answers at all.
				console.warn(
					`[lsp] ${source.languageId} semantic tokens → 0 painted at ${absolute}` +
						` (${Math.round(performance.now() - startedAt)}ms, server ${source.getState()})`,
				);
			}
			return { data: painted.data };
		},
		releaseDocumentSemanticTokens() {
			// No delta support to release: `full` is advertised as a boolean.
		},
	};
}

/**
 * Attach one pane's document to the language's provider, registering that
 * provider on first use and tearing it down with the last pane.
 */
export function registerSemanticTokens(document: SemanticDocument): SemanticRegistration {
	const { languageId, modelUri } = document;
	let entry = registered.get(languageId);
	if (!entry) {
		const emitter = new monaco.Emitter<void>();
		const documents = new Map<string, SemanticDocument>();
		const created: LanguageEntry = { documents, emitter, dispose: () => undefined };
		const disposable = monaco.languages.registerDocumentSemanticTokensProvider(languageId, provider(created));
		created.dispose = () => {
			disposable.dispose();
			emitter.dispose();
		};
		entry = created;
		registered.set(languageId, entry);
	}
	const owner = entry;
	owner.documents.set(modelUri, document);
	// The pane usually registers before the server has attached, so the first
	// fetch cannot be answered. This is the poke that brings Monaco back.
	owner.emitter.fire();

	let released = false;
	return {
		refresh: () => {
			if (!released) owner.emitter.fire();
		},
		dispose: () => {
			if (released) return;
			released = true;
			owner.documents.delete(modelUri);
			if (owner.documents.size > 0) return;
			registered.delete(languageId);
			owner.dispose();
		},
	};
}
