import type { CodeToken } from "./note/highlight";

export type CodeLine = { kind: "add" | "del" | "context"; num: number; text: string };

export type CodeView = { path: string; language: string | undefined; lines: CodeLine[]; hidden: number };

export function languageOf(_path: string): string | undefined {
	return undefined;
}

export function writeView(path: string, _content: string, _limit: number): CodeView {
	return { path, language: undefined, lines: [], hidden: 0 };
}

export function editView(
	path: string,
	_input: Record<string, unknown>,
	_result?: { structuredPatch?: unknown[] },
): CodeView {
	return { path, language: undefined, lines: [], hidden: 0 };
}

export function readView(path: string, _output: string, _limit = Number.POSITIVE_INFINITY): CodeView {
	return { path, language: undefined, lines: [], hidden: 0 };
}

export function lineCounts(_view: CodeView): { added: number; removed: number } {
	return { added: 0, removed: 0 };
}

export async function highlightView(_view: CodeView, _theme: "dark" | "light"): Promise<CodeToken[][] | undefined> {
	return undefined;
}
