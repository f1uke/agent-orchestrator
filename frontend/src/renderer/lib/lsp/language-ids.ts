/**
 * Monaco's language id → the language id the server catalogue uses.
 *
 * They agree for Go and Swift. This exists so they are allowed to disagree later
 * without a lookup table growing inside a component, and so that "this app ships
 * no server for this language" is a value the UI can render rather than a silent
 * absence.
 *
 * 🗝 Objective-C is deliberately absent even though sourcekit-lsp serves it. The
 * registry keys servers by language id, so claiming `objective-c` here would
 * spawn a SECOND sourcekit-lsp for the same iOS workspace - roughly 620 MB, for
 * a language whose files this slice was not asked to serve. ⌘click from Swift
 * INTO an Objective-C header already works and opens the file read-only.
 */
const LSP_LANGUAGES = new Set(["go", "swift"]);

export function languageIdForLsp(monacoLanguageId: string): string | null {
	return LSP_LANGUAGES.has(monacoLanguageId) ? monacoLanguageId : null;
}

/** How each language is named in the UI, so no component spells it by hand. */
const DISPLAY_NAMES: Record<string, string> = { go: "Go", swift: "Swift" };

export function languageDisplayName(languageId: string): string {
	return DISPLAY_NAMES[languageId] ?? languageId;
}

/** The server binary a reader would have to install, named in failure strings. */
const SERVER_NAMES: Record<string, string> = { go: "gopls", swift: "SourceKit-LSP" };

export function languageServerName(languageId: string): string {
	return SERVER_NAMES[languageId] ?? languageId;
}

/**
 * The extensions each served language claims, mirrored from the main-process
 * catalogue because the renderer cannot import it.
 *
 * Kept as a separate table rather than folded into `LSP_LANGUAGES` so that the
 * pairing is visible: a language in one and not the other is a bug this file
 * should make obvious to read.
 */
const LSP_LANGUAGE_EXTENSIONS: Record<string, string[]> = { go: [".go"], swift: [".swift"] };

/**
 * Which language ⌘⇧O should search symbols in, given the workspace's file index.
 *
 * 🗝 One language, not all of them. The registry caps the app at two servers, so
 * a palette that attached to every language present would evict the pane the
 * reader is looking at just to answer a search - and on an iOS project it would
 * pay ~620 MB to search a handful of stray Go files. The most-numerous served
 * language is what the reader almost certainly means.
 *
 * Returning null is a real answer: no served language is present, so nothing is
 * spawned. Without that guard ⌘⇧O in a TypeScript-only repo starts gopls in a
 * directory that has no `go.mod`, every single time it is opened.
 */
export function symbolLanguageForIndex(paths: readonly string[] | undefined): string | null {
	if (!paths || paths.length === 0) return null;
	const counts = new Map<string, number>();
	for (const path of paths) {
		const dot = path.lastIndexOf(".");
		if (dot <= 0) continue;
		const ext = path.slice(dot).toLowerCase();
		for (const [languageId, extensions] of Object.entries(LSP_LANGUAGE_EXTENSIONS)) {
			if (extensions.includes(ext)) counts.set(languageId, (counts.get(languageId) ?? 0) + 1);
		}
	}
	let best: string | null = null;
	let bestCount = 0;
	// Ties break on the language id so two indexes with the same shape always
	// choose the same server; a palette that alternates is one you cannot learn.
	for (const [languageId, count] of [...counts].sort((a, b) => (a[0] < b[0] ? -1 : 1))) {
		if (count > bestCount) {
			best = languageId;
			bestCount = count;
		}
	}
	return best;
}

/**
 * The served language for a path, straight from the extension.
 *
 * Deliberately NOT routed through Monaco's `languageForPath`: the status pill
 * lives in `WorkspaceFileView`, which lazy-loads the editor precisely so that
 * Monaco stays out of the initial bundle, and importing it for one lookup would
 * quietly undo that.
 */
export function lspLanguageForPath(path: string): string | null {
	const base = path.slice(path.lastIndexOf("/") + 1).toLowerCase();
	const dot = base.lastIndexOf(".");
	if (dot <= 0) return null;
	const ext = base.slice(dot);
	for (const [languageId, extensions] of Object.entries(LSP_LANGUAGE_EXTENSIONS)) {
		if (extensions.includes(ext)) return languageId;
	}
	return null;
}
