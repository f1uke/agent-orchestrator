import { type CodeView, splitLines } from "./chat-code";
import type { ChatItem, ToolCallItem } from "./chat-fold";
import { isObject } from "./chat-event";
import { docRefs, isDocPath } from "./chat-refs";
import type { ResolvedFile } from "./chat-refs-store";

export type DocKind = ResolvedFile["kind"];

export type FileDoc = {
	source: "file";
	root: ResolvedFile["root"];
	path: string;
	kind: DocKind;
	size: number;
	title: string;
};

/** A claude.ai artifact: it cannot be embedded, so it only links out. */
export type ArtifactDoc = { source: "artifact"; url: string; title: string };

/** What one turn names: refs for the daemon to confirm, and artifact links that need no confirming. */
export type TurnDocs = { refs: string[]; artifacts: ArtifactDoc[] };

export const DOC_LINE_LIMIT = 2000;

// The resolve endpoint refuses a body with more refs than this, or a ref longer than this.
const MAX_REFS = 64;
const MAX_REF_BYTES = 1024;

const WRITERS = new Set(["Write", "Edit", "MultiEdit"]);

/** The file a top-level Write, Edit or MultiEdit changes. A subagent's call is its own business. */
export function writtenPath(call: ToolCallItem): string | undefined {
	if (call.parentToolUseId !== "" || !WRITERS.has(call.name) || !isObject(call.input)) return undefined;
	const path = call.input.file_path;
	return typeof path === "string" && path !== "" ? path : undefined;
}

export function docsOfTurn(items: readonly ChatItem[]): string[] {
	const found = new Set<string>();
	const add = (ref: string) => {
		if (found.size < MAX_REFS && new TextEncoder().encode(ref).length <= MAX_REF_BYTES) found.add(ref);
	};
	for (const item of items) {
		if (item.kind === "tool_call") {
			const path = writtenPath(item);
			if (path !== undefined && isDocPath(path)) add(path);
		} else if (item.kind === "assistant_text" && item.parentToolUseId === "") docRefs(item.text).forEach(add);
	}
	return [...found];
}

const DEFAULT_ARTIFACT_TITLE = "claude.ai artifact";

// A link text before the URL, when the URL is a markdown link's target. The path forms are the three
// claude.ai serves an artifact on; a lookbehind keeps a longer host such as claude.ai.evil.example out.
const ARTIFACT_LINK =
	/(?:\[([^\]\n]*)\]\()?(?<![\w.-])(https:\/\/claude\.ai\/(?:artifact|public\/artifacts|code\/artifact)\/[\w-]+)/g;

/** The artifact links in a text, once each, titled by the text of the first markdown link that names them. */
export function artifactLinks(text: string): ArtifactDoc[] {
	const found = new Map<string, ArtifactDoc>();
	for (const match of text.matchAll(ARTIFACT_LINK)) {
		const url = match[2];
		const title = match[1]?.trim();
		const known = found.get(url);
		if (!known) found.set(url, { source: "artifact", url, title: title || DEFAULT_ARTIFACT_TITLE });
		else if (title && known.title === DEFAULT_ARTIFACT_TITLE) known.title = title;
	}
	return [...found.values()];
}

export function artifactsOfTurn(items: readonly ChatItem[]): ArtifactDoc[] {
	const found = new Map<string, ArtifactDoc>();
	for (const item of items) {
		if (item.kind !== "assistant_text" || item.parentToolUseId !== "") continue;
		for (const link of artifactLinks(item.text)) {
			const known = found.get(link.url);
			if (!known) found.set(link.url, link);
			else if (known.title === DEFAULT_ARTIFACT_TITLE) known.title = link.title;
		}
	}
	return [...found.values()];
}

export function turnDocs(items: readonly ChatItem[]): TurnDocs | undefined {
	const refs = docsOfTurn(items);
	const artifacts = artifactsOfTurn(items);
	return refs.length === 0 && artifacts.length === 0 ? undefined : { refs, artifacts };
}

// A row's code box already shows text, JSON, a log or a diff in full, so an Open action would only repeat it.
const ROW_KINDS: ReadonlySet<DocKind> = new Set(["markdown", "html", "pdf", "image"]);

export const rowOpens = (doc: Pick<FileDoc, "kind">): boolean => ROW_KINDS.has(doc.kind);

const baseName = (path: string) => path.split("/").filter(Boolean).at(-1) ?? path;

export function fileDocOf(match: ResolvedFile): FileDoc {
	return {
		source: "file",
		root: match.root,
		path: match.path,
		kind: match.kind,
		size: match.size,
		title: baseName(match.path),
	};
}

const KIND_TAG: Record<Exclude<DocKind, "image">, string> = {
	markdown: "MD",
	html: "HTML",
	pdf: "PDF",
	json: "JSON",
	text: "TXT",
	log: "LOG",
	diff: "DIFF",
};

export function docTag(doc: Pick<FileDoc, "kind" | "path">): string {
	if (doc.kind !== "image") return KIND_TAG[doc.kind];
	const extension = doc.path.split(".").at(-1)?.toUpperCase() ?? "";
	return extension === "JPEG" ? "JPG" : extension;
}

function sizeText(bytes: number): string {
	if (bytes < 1024) return `${bytes} B`;
	if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
	return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function docMeta(doc: Pick<FileDoc, "root" | "path" | "size">): string {
	return `${doc.root} · ${doc.path} · ${sizeText(doc.size)}`;
}

/** A text document as lines for the code box. JSON is pretty-printed when it parses and shown as written when it does not. */
export function textView(doc: Pick<FileDoc, "path" | "kind">, text: string, limit = DOC_LINE_LIMIT): CodeView {
	let body = text;
	let language: string | undefined;
	if (doc.kind === "json") {
		try {
			body = JSON.stringify(JSON.parse(text), null, 2);
			language = "json";
		} catch {
			body = text;
		}
	} else if (doc.kind === "diff") language = "diff";
	const all = splitLines(body);
	const lines = all.slice(0, limit).map((line, i) => ({ kind: "context" as const, num: i + 1, text: line }));
	return { path: doc.path, language, lines, hidden: all.length - lines.length };
}
