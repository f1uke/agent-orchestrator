import { lineDiff } from "./chat-diff";
import { isObject, type PatchHunk } from "./chat-event";
import { type CodeToken, grammarFor, highlightCode } from "./note/highlight";

export type CodeLine = { kind: "add" | "del" | "context"; num: number; text: string };

/** `hidden` counts the lines a preview cap left out. */
export type CodeView = { path: string; language: string | undefined; lines: CodeLine[]; hidden: number };

// Where an extension names a language differently from the note highlighter's own aliases.
const EXTENSION_LANGUAGE: Record<string, string> = {
	svg: "xml",
	plist: "xml",
	h: "c",
	hpp: "cpp",
	kts: "kotlin",
	mdx: "markdown",
};

export function languageOf(path: string): string | undefined {
	const name = path.split("/").at(-1) ?? "";
	const dot = name.lastIndexOf(".");
	if (dot < 0) return undefined;
	const extension = name.slice(dot + 1).toLowerCase();
	return grammarFor(EXTENSION_LANGUAGE[extension] ?? extension) || undefined;
}

function splitLines(text: string): string[] {
	return text === "" ? [] : text.replace(/\n$/, "").split("\n");
}

export function writeView(path: string, content: string, limit: number): CodeView {
	const all = splitLines(content);
	const lines = all.slice(0, limit).map((text, i): CodeLine => ({ kind: "add", num: i + 1, text }));
	return { path, language: languageOf(path), lines, hidden: all.length - lines.length };
}

type Marked = { kind: CodeLine["kind"]; text: string };

function numbered(marked: Marked[], oldStart: number, newStart: number): CodeLine[] {
	let oldLine = oldStart;
	let newLine = newStart;
	return marked.map(({ kind, text }) => {
		if (kind === "del") return { kind, num: oldLine++, text };
		if (kind === "add") return { kind, num: newLine++, text };
		oldLine++;
		return { kind, num: newLine++, text };
	});
}

const PREFIX_KIND: Record<string, CodeLine["kind"]> = { "+": "add", "-": "del" };

function patchLines(hunk: PatchHunk): CodeLine[] {
	const marked = hunk.lines
		.filter((line) => !line.startsWith("\\"))
		.map((line): Marked => ({ kind: PREFIX_KIND[line[0]] ?? "context", text: line.slice(1) }));
	return numbered(marked, hunk.oldStart, hunk.newStart);
}

function str(value: unknown): string {
	return typeof value === "string" ? value : "";
}

/**
 * The lines an Edit or MultiEdit changed. claude's `structuredPatch` carries the file's own line
 * numbers. Without one (a log from before it was kept, a MultiEdit) only the old and new strings are
 * known, so the numbers count from 1 inside the changed text.
 */
export function editView(
	path: string,
	input: Record<string, unknown>,
	result?: { structuredPatch?: PatchHunk[] },
): CodeView {
	const patch = result?.structuredPatch;
	if (patch && patch.length > 0)
		return { path, language: languageOf(path), lines: patch.flatMap(patchLines), hidden: 0 };
	const edits = Array.isArray(input.edits)
		? input.edits.filter(isObject).map((e) => [str(e.old_string), str(e.new_string)] as const)
		: [[str(input.old_string), str(input.new_string)] as const];
	const lines = edits.flatMap(([before, after]) => numbered(lineDiff(before, after) as Marked[], 1, 1));
	return { path, language: languageOf(path), lines, hidden: 0 };
}

const CAT_N = /^\s*(\d+)\t(.*)$/;

/** Parses the `cat -n` text of a Read: `    12\ttext`. It stops at the first line without a number prefix. */
export function readView(path: string, output: string, limit = Number.POSITIVE_INFINITY): CodeView {
	const lines: CodeLine[] = [];
	let total = 0;
	for (const raw of output.split("\n")) {
		const match = CAT_N.exec(raw);
		if (!match) break;
		total += 1;
		if (lines.length < limit) lines.push({ kind: "context", num: Number(match[1]), text: match[2] });
	}
	return { path, language: languageOf(path), lines, hidden: total - lines.length };
}

export function lineCounts(view: CodeView): { added: number; removed: number } {
	let added = 0;
	let removed = 0;
	for (const line of view.lines) {
		if (line.kind === "add") added += 1;
		else if (line.kind === "del") removed += 1;
	}
	return { added, removed };
}

/**
 * Colours a view with shiki. The old text and the new text are each highlighted as a whole, so a
 * line inside a multi-line string or comment takes its colour from the lines around it, then the
 * tokens are dealt back to the view's lines. One token list per line, or undefined when the file has
 * no grammar.
 */
export async function highlightView(view: CodeView, theme: "dark" | "light"): Promise<CodeToken[][] | undefined> {
	if (!view.language) return undefined;
	const text = (keep: (line: CodeLine) => boolean) =>
		view.lines
			.filter(keep)
			.map((line) => line.text)
			.join("\n");
	const after = await highlightCode(
		text((l) => l.kind !== "del"),
		view.language,
		theme,
	);
	const before = view.lines.some((l) => l.kind === "del")
		? await highlightCode(
				text((l) => l.kind !== "add"),
				view.language,
				theme,
			)
		: after;
	if (!before || !after) return undefined;
	let oldAt = 0;
	let newAt = 0;
	return view.lines.map((line) => {
		if (line.kind === "del") return before[oldAt++] ?? [];
		if (line.kind === "add") return after[newAt++] ?? [];
		oldAt += 1;
		return after[newAt++] ?? [];
	});
}
