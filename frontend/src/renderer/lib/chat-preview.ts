import type { ChatItem } from "./chat-fold";
import { writtenPath } from "./chat-docs";

// No host source and no connect-src: the file's own script runs, but nothing in it can reach the daemon or
// the network. A policy the file adds itself can only narrow this one.
export const PREVIEW_CSP = [
	"default-src 'none'",
	"script-src 'unsafe-inline'",
	"style-src 'unsafe-inline'",
	"img-src data: blob:",
	"font-src data:",
	"media-src data: blob:",
	"base-uri 'none'",
	"form-action 'none'",
	"frame-src 'none'",
].join("; ");

const POLICY = `<meta http-equiv="Content-Security-Policy" content="${PREVIEW_CSP}">`;

const DOCTYPE = /^\s*<!doctype[^>]*>/i;

export function previewDocument(html: string): string {
	const doctype = DOCTYPE.exec(html)?.[0];
	return doctype ? `${doctype}${POLICY}${html.slice(doctype.length)}` : `${POLICY}${html}`;
}

const segments = (path: string) => path.split("/").filter(Boolean);

// The model writes an absolute path and the daemon answers with one relative to its root, so the
// written path matches when it ends in every segment of the document's.
function names(written: string, path: string): boolean {
	const tail = segments(path);
	const all = segments(written);
	return tail.length > 0 && all.length >= tail.length && all.slice(-tail.length).join("/") === tail.join("/");
}

export function writeRevision(items: readonly ChatItem[], doc: { path: string }): number {
	let count = 0;
	for (const item of items) {
		if (item.kind !== "tool_call" || !item.result || item.result.isError) continue;
		const written = writtenPath(item);
		if (written !== undefined && names(written, doc.path)) count++;
	}
	return count;
}
