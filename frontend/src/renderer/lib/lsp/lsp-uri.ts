import type { WorkspaceFileOpen } from "../open-workspace-file";

/**
 * The translation layer between what a language server says and what this app's
 * file-open seam takes.
 *
 * Three URI worlds meet here. Monaco's models use the app's own `ao-file:`
 * scheme (see `MonacoFileEditor`), a language server speaks `file:`, and
 * `WorkspaceFileOpen` speaks paths. Keeping the conversion in one pure module is
 * what stops each of the three growing its own half-right version.
 */

/** `/a/b c.go` → `file:///a/b%20c.go`. */
export function fileUriForPath(absolutePath: string): string {
	// `encodeURI` leaves `/` alone and escapes spaces and non-ASCII. `#` and `?`
	// survive it, and would then be read as a fragment or a query, so they are
	// escaped by hand - a path containing either is rare and silently wrong.
	return `file://${encodeURI(absolutePath).replace(/#/g, "%23").replace(/\?/g, "%3F")}`;
}

/**
 * Where a server's documents live, when that is not where the files live.
 *
 * `documentRoot` comes from the attachment, and equals `workspaceRoot` for every
 * language but Swift.
 */
export type DocumentMapping = { workspaceRoot: string; documentRoot: string };

/**
 * The URI to ADDRESS a workspace file by when talking to a language server.
 *
 * 🗝 Swift on an Xcode project is served from a shadow root under the AO data
 * dir - the alternative is writing `buildServer.json` into the user's checkout -
 * and sourcekit-lsp WILL NOT SERVE A DOCUMENT THAT LIES OUTSIDE ITS ROOT. The
 * shadow root therefore holds a symlink to the checkout, and documents are
 * addressed through it.
 *
 * Measured on a real iOS app, with the shadow root correct in every other
 * respect: address the same file by its real path instead and all four ⌘click
 * targets return 0 hits in 55-474 ms, with no error and no diagnostic, while
 * `workspace/symbol` keeps returning the right answers - because symbols come
 * from the index store and never touch the document's URI. So the failure this
 * function prevents does not look like a failure; it looks like ⌘click quietly
 * not being implemented.
 *
 * Only the OUTGOING direction needs this. sourcekit-lsp resolves the link for
 * its replies, so definitions and symbols come back as real paths.
 */
export function documentUriForPath(absolutePath: string, mapping: DocumentMapping): string {
	const workspaceRoot = mapping.workspaceRoot.replace(/\/+$/, "");
	const documentRoot = mapping.documentRoot.replace(/\/+$/, "");
	if (documentRoot === workspaceRoot || workspaceRoot === "") return fileUriForPath(absolutePath);
	// A path outside the workspace - a definition in a Pod from another checkout,
	// a knowledge-store note - has no place under the document root and is sent
	// as-is. The server will decline it, which is the correct outcome and a
	// visible one.
	if (absolutePath !== workspaceRoot && !absolutePath.startsWith(`${workspaceRoot}/`)) {
		return fileUriForPath(absolutePath);
	}
	return fileUriForPath(documentRoot + absolutePath.slice(workspaceRoot.length));
}

/** `file:///a/b%20c.go` → `/a/b c.go`. */
export function pathForFileUri(uri: string): string {
	const withoutScheme = uri.replace(/^file:\/\//, "");
	try {
		return decodeURIComponent(withoutScheme);
	} catch {
		// A URI we cannot decode is better handled as-is than dropped: the open
		// then fails visibly, with a path a human can read.
		return withoutScheme;
	}
}

/**
 * Turn a definition or symbol target into the seam's shape.
 *
 * `inWorkspace` is normally the SERVER's containment verdict and must never be
 * re-derived from a path's shape - but here there is no server to ask: the
 * language server answered with an absolute path, and the only fact available is
 * the workspace root that the same process spawned it with. Comparing against
 * that root with a trailing separator is the honest version of it, and the
 * separator is what stops `/…/feature-a-old` reading as inside `/…/feature-a`.
 */
export function openTargetForUri(input: {
	uri: string;
	workspaceRoot: string;
	line?: number;
	column?: number;
}): WorkspaceFileOpen {
	const absolute = pathForFileUri(input.uri);
	const root = input.workspaceRoot.endsWith("/") ? input.workspaceRoot.slice(0, -1) : input.workspaceRoot;
	const inWorkspace = absolute.startsWith(`${root}/`);
	return {
		path: inWorkspace ? absolute.slice(root.length + 1) : absolute,
		line: input.line,
		column: input.column,
		inWorkspace,
	};
}
