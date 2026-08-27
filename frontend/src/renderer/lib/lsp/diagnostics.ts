import { monaco } from "../monaco-setup";
import { countMarkers, type PublishDiagnosticsParams, toMonacoMarkers } from "./diagnostics-mapping";
import type { LspClient } from "./lsp-client";
import { pathForFileUri } from "./lsp-uri";
import { modelUriForPath } from "./peek-sources";

/**
 * Errors and warnings in the file, from `textDocument/publishDiagnostics`.
 *
 * ## The data was already arriving, and this app threw every one away
 *
 * Both servers publish unprompted — there is no request to make and no
 * capability to negotiate beyond the one `lsp-process.ts` already declares. The
 * only thing missing was a door: `lsp-client.ts` returned early on any message
 * carrying a `method`, so every diagnostic this app has ever received went
 * straight in the bin. `onNotification` is that door.
 *
 * ## 🗝 Diagnostics arriving is NOT evidence the server works
 *
 * Slice 5 measured an UNCONFIGURED sourcekit-lsp: it initialises in 61 ms,
 * publishes diagnostics, answers `documentSymbol` — and returns 0 hits for every
 * ⌘click and every symbol query. So nothing here feeds readiness, nothing here
 * moves the status pill, and the header must never render a squiggle-free file
 * as a verdict. Slice 3's three states remain the whole vocabulary.
 *
 * ## …and the first publish is a lie on gopls
 *
 * Measured on this repo's `backend/`, from `didOpen`:
 *
 * | | gopls | sourcekit-lsp |
 * |---|---|---|
 * | first publish | **0 items at ~932 ms** | 3 items at ~3 325 ms |
 * | second publish | **3 items at ~5 010 ms** | — |
 * | after an edit | 1 publish, ~840 ms later | 1 publish, ~4 611 ms later |
 * | carries `version` | yes | **no — `undefined` every time** |
 *
 * gopls's first publish is EMPTY and lands four seconds before the real one. A
 * header that renders it as "no problems" is lying for four seconds — which is
 * why `countMarkers`' zero is never shown as a verdict, only a non-zero count as
 * a fact.
 *
 * ## What Monaco does with this, all of it for free
 *
 * `setModelMarkers` is the whole rendering: `markerDecorationsService.js` turns
 * markers into squiggles, overview-ruler marks and minimap marks, and
 * `markerHoverParticipant.js` puts the message inside the same hover widget the
 * type information uses. None of that was written here, and none of it needed
 * to be.
 */

export type DiagnosticsDocument = {
	languageId: string;
	client: LspClient;
	model: monaco.editor.ITextModel;
	/** The server's OWN address for this document — `DocumentSync.uri`. */
	uri: string;
	/** The counts, for a header that shows them only when they are not zero. */
	onCounts?: (counts: { errors: number; warnings: number }) => void;
};

/**
 * One owner per language.
 *
 * Monaco keys markers by (resource, owner) and REPLACES the whole set per owner,
 * so a per-language owner means a re-attached server's publish supersedes its
 * predecessor's instead of doubling every squiggle — and means this app's
 * markers can never collide with another source's.
 */
function ownerFor(languageId: string): string {
	return `lsp:${languageId}`;
}

type ClientEntry = {
	/** Keyed by the DECODED path, so two spellings of one `file:` URI still meet. */
	documents: Map<string, DiagnosticsDocument>;
	unsubscribe: () => void;
	/** The last version acted on, per document. Only gopls ever supplies one. */
	applied: Map<string, number>;
};

/**
 * One notification listener per CLIENT, not per document.
 *
 * 🗝 Which is what makes the miss visible. With a listener per document, each
 * one filters on its own URI and a publish addressed to a file nobody
 * recognises is dropped by every listener in silence — and that is precisely the
 * shape of slice 5's Swift trap, where the document is addressed through a
 * shadow-root symlink and getting the path wrong produces no error anywhere. One
 * dispatcher can see that nothing matched, and say so.
 */
const clients = new Map<LspClient, ClientEntry>();

/** One line per unmatched target per client, not one per publish. */
const warnedTargets = new WeakMap<LspClient, Set<string>>();

function keyOf(uri: string): string {
	return pathForFileUri(uri);
}

function dispatch(client: LspClient, entry: ClientEntry, params: unknown): void {
	const payload = params as PublishDiagnosticsParams | undefined;
	const target = payload?.uri;
	if (typeof target !== "string") return;
	const key = keyOf(target);
	const document = entry.documents.get(key);
	if (!document) {
		// Not an error: a server may legitimately publish for a file we never
		// opened — gopls does it for other files in a package it is loading. Said
		// once per file so that the case which IS a bug (every publish addressed to
		// a path this app cannot match, which is what a wrong Swift document root
		// produces) is visible rather than perfectly silent.
		let seen = warnedTargets.get(client);
		if (!seen) {
			seen = new Set();
			warnedTargets.set(client, seen);
		}
		if (!seen.has(key)) {
			seen.add(key);
			console.warn(`[lsp] diagnostics for a document this pane did not open: ${key}`);
		}
		return;
	}

	// 🗝 An OUT-OF-ORDER publish, not a stale one. gopls sends two per open and
	// they could in principle arrive reordered; applying the older one would
	// leave the file showing diagnostics the server has already retracted. It
	// deliberately does NOT drop a publish merely because the buffer has moved on
	// since: Monaco's marker decorations shift with each edit, so an in-flight
	// diagnostic stays on its line while you type, and withholding it would leave
	// the file blank for the 840 ms (Go) to 4 611 ms (Swift) the server takes to
	// answer again.
	const version = typeof payload?.version === "number" ? payload.version : null;
	if (version !== null) {
		const applied = entry.applied.get(key);
		if (applied !== undefined && version < applied) return;
		entry.applied.set(key, version);
	}

	const markers = toMonacoMarkers(payload?.diagnostics, (related) => {
		// Related information points at a REAL path; the models it has to address
		// are this app's own `ao-file:` ones. An unaddressable one is dropped by
		// the mapper rather than rendered as a row that goes nowhere.
		try {
			return modelUriForPath(pathForFileUri(related));
		} catch {
			return null;
		}
	});
	if (document.model.isDisposed()) return;
	monaco.editor.setModelMarkers(document.model, ownerFor(document.languageId), markers);
	document.onCounts?.(countMarkers(markers));
}

export function registerDiagnostics(document: DiagnosticsDocument): monaco.IDisposable {
	const { client, model, uri, languageId } = document;
	const key = keyOf(uri);
	let entry = clients.get(client);
	if (!entry) {
		const created: ClientEntry = { documents: new Map(), applied: new Map(), unsubscribe: () => undefined };
		created.unsubscribe = client.onNotification("textDocument/publishDiagnostics", (params) =>
			dispatch(client, created, params),
		);
		clients.set(client, created);
		entry = created;
	}
	entry.documents.set(key, document);

	let released = false;
	return {
		dispose() {
			if (released) return;
			released = true;
			const owner = clients.get(client);
			if (owner) {
				owner.documents.delete(key);
				owner.applied.delete(key);
				if (owner.documents.size === 0) {
					owner.unsubscribe();
					clients.delete(client);
				}
			}
			// 🗝 CLEARED, not left behind. This registration is keyed on the client,
			// so it is torn down when the server stops, is evicted or fails — and
			// squiggles from a server that is no longer running are the most
			// confident kind of wrong answer this app could give.
			if (!model.isDisposed()) monaco.editor.setModelMarkers(model, ownerFor(languageId), []);
			document.onCounts?.({ errors: 0, warnings: 0 });
		},
	};
}
