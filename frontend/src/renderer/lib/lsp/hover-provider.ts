import { monaco } from "../monaco-setup";
import { toMonacoHover } from "./hover-mapping";
import { languageServerName } from "./language-ids";
import type { LspClient } from "./lsp-client";
import { runInLane, withTimeout } from "./request-lane";

/**
 * The Monaco half of hover: the type under the pointer.
 *
 * ## 🗝 Hover is NOT per-mouse-move, and that decides the whole design
 *
 * The obvious worry — "the pointer moves far more often than a key is pressed,
 * so hover will flood the server" — is false in this editor, and it was checked
 * in monaco-editor 0.56's own sources rather than assumed.
 * `contrib/hover/browser/hoverOperation.js` schedules the ASYNC computation (the
 * call into here) on `_firstWaitTime`, which is `editor.hover.delay / 2`;
 * `editorOptions.js:899` defaults that delay to **300 ms**. So the provider is
 * asked **150 ms after the pointer has come to rest**, never while it is moving,
 * and `HoverOperation.cancel()` drops the pending async iterable the instant it
 * moves again. **Monaco already debounces this.** Adding a debounce here would
 * stack a second one on top of it and make the first hover of a session late for
 * no reason.
 *
 * ## The pacing, measured rather than carried over on faith
 *
 * | | first request in the file | warm repeats |
 * |---|---|---|
 * | sourcekit-lsp | **1 919 ms** | 56–70 ms |
 * | gopls | **587 ms** | 0–17 ms |
 *
 * Same shape as completion: one expensive event per file — the file's first
 * type-check — then cheap. And the cancel question, asked again for hover rather
 * than assumed from #258. The pointer resting at 8 successive positions 300 ms
 * apart (Monaco's own minimum spacing), on a cold file, each policy twice:
 *
 * | server | policy | last answer |
 * |---|---|---|
 * | sourcekit-lsp | + `$/cancelRequest` | 2 410 / 2 409 ms |
 * | sourcekit-lsp | **serialise, never cancel** | **2 404 / 2 404 ms** |
 * | gopls | + `$/cancelRequest` | 2 405 / 2 410 ms |
 * | gopls | **serialise, never cancel** | **2 401 / 2 403 ms** |
 *
 * Cancelling neither helps nor hurts the answer the reader is waiting for — the
 * sweep's own duration dominates both. It is not free, though: sourcekit-lsp
 * refused **6 of the 8** with `-32800`, so a reader who paused briefly at an
 * intermediate word got nothing, while gopls refused none at all — the two
 * servers do not even agree on what a cancel means. And #258 measured that each
 * cancellation throws away the in-progress type-check that hover #1 is paying
 * 1 919 ms for.
 *
 * **So: the completion policy carries over unchanged, and hover SHARES its lane**
 * (see `request-lane.ts`) — because both are waiting on the same first
 * type-check, and two lanes would pay for it twice.
 *
 * ## And it fails by returning nothing, loudly enough to find
 *
 * `getHover.js:22` hands a rejected `provideHover` to `onUnexpectedExternalError`,
 * exactly as `suggest.js:205` does for completion — a stack trace on the global
 * error channel per pointer rest while a server starts. So every path that
 * cannot answer returns `null`.
 */

export type HoverDocument = {
	languageId: string;
	/** The model this pane is showing, so the provider answers per document. */
	modelUri: string;
	getClient: () => LspClient | null;
	getAbsolutePath: () => string | null;
	/** The text the SERVER has — see `document-sync.ts`. Null when nothing is synced. */
	getServerText: () => string | null;
	/** Slice 3's state, so a 0-hit answer can say WHY. */
	getState: () => string;
};

/**
 * The open panes per language, held OUTSIDE any registration for the reason
 * `completion-provider.ts` spells out: Monaco's provider registry is GLOBAL and
 * asks one provider about EVERY model of that language, so two Swift panes are
 * the ordinary case and a closure over one path would answer for the other.
 */
const documentsByLanguage = new Map<string, Map<string, HoverDocument>>();
const registered = new Map<string, { documents: Map<string, HoverDocument>; dispose: () => void }>();

/** Which provider call is the current one, per document. Per FEATURE — see `request-lane.ts`. */
const generations = new Map<string, number>();

function documentsFor(languageId: string): Map<string, HoverDocument> {
	let documents = documentsByLanguage.get(languageId);
	if (!documents) {
		documents = new Map();
		documentsByLanguage.set(languageId, documents);
	}
	return documents;
}

/**
 * 🗝 `null`, never a throw and never an empty hover.
 *
 * A throw reaches `onUnexpectedExternalError`. An empty hover is indistinguishable
 * from no hover to Monaco (`getHover.js:51`'s `isValid` requires both a range and
 * non-empty contents), so it buys nothing and loses the one place that can still
 * tell the two apart — which is where the log line goes.
 */
const NO_ANSWER = null;

function provider(documents: Map<string, HoverDocument>): monaco.languages.HoverProvider {
	return {
		async provideHover(model, position, token) {
			const source = documents.get(model.uri.toString());
			if (!source) return NO_ANSWER;
			const client = source.getClient();
			const absolute = source.getAbsolutePath();
			// Quiet on purpose. The reader did not ASK a question — they moved a
			// pointer — and the status pill is already saying the server is starting.
			// A console line per pointer rest is what makes a real warning unreadable.
			if (!client || !absolute) return NO_ANSWER;
			// The server exists and named no hover provider. Distinct from "not yet
			// attached", and worth exactly one line rather than one per rest.
			if (!client.features().hover) {
				warnOnce(`${languageServerName(source.languageId)} offers no hover for ${source.languageId} files`);
				return NO_ANSWER;
			}
			if (source.getServerText() === null) return NO_ANSWER;

			const generation = (generations.get(source.modelUri) ?? 0) + 1;
			generations.set(source.modelUri, generation);
			const stale = () => token.isCancellationRequested || generations.get(source.modelUri) !== generation;

			const startedAt = performance.now();
			let result: unknown;
			try {
				const outcome = await runInLane(source.modelUri, stale, () =>
					withTimeout(() =>
						client.request<unknown>("textDocument/hover", {
							// 🗝 `client.documentUri`, never `fileUriForPath`. On Swift the
							// two differ and the wrong one answers nothing, with no error.
							textDocument: { uri: client.documentUri(absolute) },
							position: { line: position.lineNumber - 1, character: position.column - 1 },
						}),
					),
				);
				if (!outcome.ok) return NO_ANSWER;
				result = outcome.value;
			} catch (err) {
				// A request that FAILED is always worth a line: it is the difference
				// between a server with nothing to say and a server that is broken.
				console.warn(`[lsp] ${source.languageId} hover failed at ${absolute}:${position.lineNumber}`, err);
				return NO_ANSWER;
			}

			// Checked again after the answer: the lane's own check only runs for a
			// call that actually waited.
			if (stale()) return NO_ANSWER;

			const hover = toMonacoHover(result);
			if (!hover) {
				// Up, answering, and answering NOTHING — which for hover is often
				// correct (whitespace, a comment) and is exactly why the state is part
				// of the sentence: on a cold Swift file the first hover costs ~1.9 s,
				// and "nothing here" during that window is the type-check, not the code.
				console.warn(
					`[lsp] ${source.languageId} hover → nothing at ${absolute}:${position.lineNumber}:${position.column}` +
						` (${Math.round(performance.now() - startedAt)}ms, server ${source.getState()})`,
				);
				return NO_ANSWER;
			}
			return hover;
		},
	};
}

/** One line per reason per session. A capability does not change under a client. */
const warned = new Set<string>();
function warnOnce(message: string): void {
	if (warned.has(message)) return;
	warned.add(message);
	console.warn(`[lsp] ${message}`);
}

/**
 * Attach one pane's document to the language's hover provider, registering that
 * provider on first use.
 *
 * Unlike completion there is nothing static to re-register for: a hover provider
 * carries no trigger characters, so the capability is read per request off the
 * client rather than baked into the registration.
 */
export function registerHover(document: HoverDocument): monaco.IDisposable {
	const { languageId, modelUri } = document;
	const documents = documentsFor(languageId);
	documents.set(modelUri, document);
	if (!registered.has(languageId)) {
		const disposable = monaco.languages.registerHoverProvider(languageId, provider(documents));
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
