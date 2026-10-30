import type { components } from "../../api/schema";

export type DiffLine = components["schemas"]["DiffContextLineDTO"];

const MAX_LCS_LINES = 600;

function splitLines(text: string): string[] {
	if (text === "") return [];
	return text.replace(/\n$/, "").split("\n");
}

export function lineDiff(before: string, after: string): DiffLine[] {
	const a = splitLines(before);
	const b = splitLines(after);
	const del = (text: string): DiffLine => ({ kind: "del", oldLine: 0, newLine: 0, text });
	const add = (text: string): DiffLine => ({ kind: "add", oldLine: 0, newLine: 0, text });
	if (a.length > MAX_LCS_LINES || b.length > MAX_LCS_LINES) return [...a.map(del), ...b.map(add)];

	const lcs = Array.from({ length: a.length + 1 }, () => new Uint16Array(b.length + 1));
	for (let i = a.length - 1; i >= 0; i--) {
		for (let j = b.length - 1; j >= 0; j--) {
			lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
		}
	}
	const out: DiffLine[] = [];
	let i = 0;
	let j = 0;
	while (i < a.length || j < b.length) {
		if (i < a.length && j < b.length && a[i] === b[j]) {
			out.push({ kind: "context", oldLine: 0, newLine: 0, text: a[i] });
			i++;
			j++;
		} else if (i < a.length && (j === b.length || lcs[i + 1][j] >= lcs[i][j + 1])) {
			out.push(del(a[i++]));
		} else {
			out.push(add(b[j++]));
		}
	}
	return out;
}

export function newFileDiff(content: string, limit: number): { lines: DiffLine[]; hidden: number } {
	const all = splitLines(content);
	const lines = all.slice(0, limit).map((text, i): DiffLine => ({ kind: "add", oldLine: 0, newLine: i + 1, text }));
	return { lines, hidden: all.length - lines.length };
}

export function diffCounts(lines: readonly DiffLine[]): { added: number; removed: number } {
	let added = 0;
	let removed = 0;
	for (const line of lines) {
		if (line.kind === "add") added++;
		else if (line.kind === "del") removed++;
	}
	return { added, removed };
}
