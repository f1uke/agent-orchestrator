import type { CodeView } from "./chat-code";
import type { ChatItem, ToolCallItem } from "./chat-fold";
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

export type ArtifactDoc = { source: "artifact"; url: string; title: string };

export type TurnDocs = { refs: string[]; artifacts: ArtifactDoc[] };

export const DOC_LINE_LIMIT = 2000;

export function writtenPath(_call: ToolCallItem): string | undefined {
	return undefined;
}

export function docsOfTurn(_items: readonly ChatItem[]): string[] {
	return [];
}

export function artifactLinks(_text: string): ArtifactDoc[] {
	return [];
}

export function artifactsOfTurn(_items: readonly ChatItem[]): ArtifactDoc[] {
	return [];
}

export function turnDocs(_items: readonly ChatItem[]): TurnDocs | undefined {
	return undefined;
}

export const rowOpens = (_doc: Pick<FileDoc, "kind">): boolean => false;

export function fileDocOf(match: ResolvedFile): FileDoc {
	return { source: "file", root: match.root, path: "", kind: match.kind, size: 0, title: "" };
}

export function docTag(_doc: Pick<FileDoc, "kind" | "path">): string {
	return "";
}

export function docMeta(_doc: Pick<FileDoc, "root" | "path" | "size">): string {
	return "";
}

export function textView(doc: Pick<FileDoc, "path" | "kind">, _text: string, _limit = DOC_LINE_LIMIT): CodeView {
	return { path: doc.path, language: undefined, lines: [], hidden: 0 };
}
