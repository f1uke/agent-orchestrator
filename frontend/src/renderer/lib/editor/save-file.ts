import type { components } from "../../../api/schema";

export type SaveRequest = components["schemas"]["WriteWorkspaceFileRequest"];

/**
 * Build the body for `PUT .../workspace/file`, refusing anything the route
 * would rightly reject.
 *
 * 🗝 The `content` guard is not defensive politeness — this client is the exact
 * caller the daemon's own data-loss bug was found for. A `content` of
 * `undefined` is DROPPED by `JSON.stringify`, and `null` is the ordinary way to
 * spell "the Monaco model has not loaded yet"; before the route was hardened,
 * a body with no `content` emptied the file and answered 200. The server now
 * refuses both with a 400, and this refuses them one layer earlier so nobody
 * ever sees that error at all.
 *
 * Emptying a file stays possible, and must be spelled as what it is: an
 * explicit empty string.
 */
export function buildSaveRequest(input: { path: string; text: unknown; baseHash: string | undefined }): SaveRequest {
	if (typeof input.text !== "string") {
		throw new Error("Refusing to save: the editor has no content for this file yet.");
	}
	if (!input.baseHash) {
		// There is deliberately no way to spell "write regardless", so a save with
		// no base hash is not a request the route can serve - it is a client bug,
		// and it must fail here rather than as a puzzling 400.
		throw new Error("Refusing to save: this file was read without a content hash to write against.");
	}
	if (input.path.startsWith("/") || input.path.startsWith("~")) {
		throw new Error("Refusing to save: files outside this session's workspace are read-only.");
	}
	return { path: input.path, content: input.text, baseHash: input.baseHash };
}

/**
 * The file's exact bytes, as the server will write them.
 *
 * The read response splits on `\n` after trimming a final one, so whether the
 * file ended in a newline is not recoverable from `lines` alone — the spike's
 * save silently dropped it, turning a one-line change into a two-line diff. The
 * read carries `trailingNewline` for exactly this, and the server normalises
 * nothing, so putting it back is the client's job.
 */
export function fileBytes(modelText: string, trailingNewline: boolean): string {
	if (!trailingNewline) return modelText;
	return modelText.endsWith("\n") ? modelText : `${modelText}\n`;
}

/** The model text a file's bytes imply, i.e. the inverse of `fileBytes`. */
export function modelTextFrom(lines: readonly { text: string }[]): string {
	return lines.map((l) => l.text).join("\n");
}
