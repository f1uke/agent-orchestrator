import { languageForPath, monaco } from "../monaco-setup";
import { fileUriForPath } from "./lsp-uri";

/**
 * The models Monaco's peek widgets read their preview text out of.
 *
 * ## 🗝 The thing Monaco does NOT give you
 *
 * Peek definition (⌥F12), Peek References and the ⌘-hover definition preview are
 * all already built — `contrib/gotoSymbol/browser/*`, registered by the barrel
 * import `monaco-setup.ts` insists on. What is not built is the ability to show
 * a file you are not already looking at. In monaco-editor 0.56,
 * `standaloneServices.js:127`:
 *
 * ```js
 * createModelReference(resource) {
 *   const model = this.modelService.getModel(resource);
 *   if (!model) { return Promise.reject(new Error(`Model not found`)); }
 * ```
 *
 * A synchronous lookup with no way to fetch. So out of the box every peek at a
 * target outside the current file opens with an EMPTY preview pane: the tree row
 * degrades gracefully to `File.swift:12:5` (`referencesTree.js:155`), the
 * preview editor does not, and `_revealReference` awaits a rejected promise.
 *
 * 🗝 And it bites a target in the SAME file too, which is the part that is easy
 * to miss. A pane's model is `ao-file:///<session>/<relative path>`, while a
 * server's answer is mapped to `ao-file://<absolute path>` — two different URIs
 * for one file, so the lookup misses even when the model is right there. That is
 * what `registerPaneModel` fixes: a target inside an open pane previews the
 * pane's LIVE buffer, unsaved edits included, rather than a second copy of the
 * saved text.
 *
 * ## What this costs, and the cap
 *
 * Measured before it was written: `textDocument/references` on a real workspace
 * answers **164 hits across 35 files** (gopls, `Service` in this repo's backend)
 * and **69 hits across 32 files** (sourcekit-lsp, a protocol in the iOS app), in
 * 7–88 ms. The request is never the expensive half; materialising previews is.
 * So the files are read in parallel with a bounded fan-out and a hard cap, and
 * what the cap left out is LOGGED rather than left to look like empty files.
 */

/** A pane's own model, so a peek at the file you are in shows the live buffer. */
const paneModels = new Map<string, string>();

/** Models this module created, newest last. Only these are ever disposed. */
const owned = new Map<string, monaco.editor.ITextModel>();

/**
 * How many preview models to keep.
 *
 * Comfortably above the largest real answer measured (35 files) so an ordinary
 * peek keeps every preview, and low enough that a session spent peeking does not
 * accumulate models for the rest of its life. Eviction only ever touches models
 * this module created — a pane's own model is not ours to dispose.
 */
const PREVIEW_MODEL_LIMIT = 80;

/** How many files to read at once. The daemon is local; this is politeness, not throughput. */
const READ_CONCURRENCY = 8;

/**
 * How many distinct files one peek will materialise.
 *
 * Past this the rows still render — Monaco's own `File.swift:12:5` fallback —
 * and the shortfall is logged. Chosen above every real answer measured; a result
 * spanning more files than this is not a list anyone reads, it is a search.
 */
export const PREVIEW_FILE_LIMIT = 60;

/** The URI a pane's model uses, so peek previews land on the buffer being edited. */
export function registerPaneModel(absolutePath: string, modelUri: string): () => void {
	paneModels.set(absolutePath, modelUri);
	return () => {
		if (paneModels.get(absolutePath) === modelUri) paneModels.delete(absolutePath);
	};
}

/**
 * The model URI to address a file by.
 *
 * A pane's own model wins; otherwise the `ao-file:` spelling of the absolute
 * path, which is the same one `definition.ts` has always produced.
 */
export function modelUriForPath(absolutePath: string): monaco.Uri {
	const pane = paneModels.get(absolutePath);
	if (pane) return monaco.Uri.parse(pane);
	return monaco.Uri.parse(fileUriForPath(absolutePath).replace(/^file:\/\//, "ao-file://"));
}

/** `ao-file://…` → the absolute path, for a target that came back from a server. */
export function modelUriForTargetUri(fileUri: string): monaco.Uri {
	return modelUriForPath(decodeUri(fileUri));
}

function decodeUri(fileUri: string): string {
	const withoutScheme = fileUri.replace(/^file:\/\//, "");
	try {
		return decodeURIComponent(withoutScheme);
	} catch {
		return withoutScheme;
	}
}

function evict(): void {
	while (owned.size > PREVIEW_MODEL_LIMIT) {
		const oldest = owned.keys().next();
		if (oldest.done) return;
		const model = owned.get(oldest.value);
		owned.delete(oldest.value);
		if (model && !model.isDisposed()) model.dispose();
	}
}

/**
 * Make sure every one of `absolutePaths` can be resolved to a Monaco model,
 * creating one from `read` for the files that have none.
 *
 * Returns how many files were left without a preview, so the caller can say so.
 * A file that fails to read (too large, binary, deleted since the server indexed
 * it) counts as one of those: an empty model would render as an empty file,
 * which is a worse lie than Monaco's own `path:line:col` fallback.
 */
export async function ensurePreviewModels(
	absolutePaths: readonly string[],
	read: (absolutePath: string) => Promise<string | null>,
): Promise<{ requested: number; missing: number }> {
	const wanted: string[] = [];
	for (const absolute of absolutePaths) {
		const uri = modelUriForPath(absolute);
		const existing = monaco.editor.getModel(uri);
		if (existing && !existing.isDisposed()) {
			// Touch it, so a model that is still being previewed is not the next one
			// evicted. A pane's model is not in `owned` and is untouched by this.
			const held = owned.get(uri.toString());
			if (held) {
				owned.delete(uri.toString());
				owned.set(uri.toString(), held);
			}
			continue;
		}
		wanted.push(absolute);
	}

	let missing = 0;
	const capped = wanted.slice(0, PREVIEW_FILE_LIMIT);
	missing += wanted.length - capped.length;

	const queue = [...capped];
	const workers = Array.from({ length: Math.min(READ_CONCURRENCY, queue.length) }, async () => {
		for (;;) {
			const absolute = queue.shift();
			if (absolute === undefined) return;
			let text: string | null = null;
			try {
				text = await read(absolute);
			} catch {
				text = null;
			}
			if (text === null) {
				missing++;
				continue;
			}
			const uri = modelUriForPath(absolute);
			// Re-checked after the await: a pane may have opened this very file while
			// the read was out, and creating a second model on one URI throws.
			const existing = monaco.editor.getModel(uri);
			if (existing && !existing.isDisposed()) continue;
			const model = monaco.editor.createModel(text, languageForPath(absolute), uri);
			owned.set(uri.toString(), model);
		}
	});
	await Promise.all(workers);
	evict();
	return { requested: absolutePaths.length, missing };
}

/** For tests, and for a hard reset. Disposes only the models this module created. */
export function disposePreviewModels(): void {
	for (const model of owned.values()) if (!model.isDisposed()) model.dispose();
	owned.clear();
}
