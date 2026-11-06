import { type CodeView, languageOf, splitLines } from "./chat-code";
import type { ChatItem, ToolCallItem } from "./chat-fold";
import { isObject } from "./chat-event";
import { docRefs, isShowablePath, mediaRefs } from "./chat-refs";
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

const addRef = (found: Set<string>, ref: string) => {
	if (found.size < MAX_REFS && new TextEncoder().encode(ref).length <= MAX_REF_BYTES) found.add(ref);
};

// A word is split into parts so quoting can decide what expands: a part is literal when single quotes or a
// backslash keep it as written.
type Part = { text: string; literal: boolean };
type Word = Part[];

/** Splits a shell script into its simple commands, each a list of words. Operators inside quotes stay in their word. */
function simpleCommands(script: string): Word[][] {
	const commands: Word[][] = [];
	let words: Word[] = [];
	let word: Word = [];
	let started = false;
	let quote: string | undefined;
	const push = (text: string, literal: boolean) => {
		const last = word.at(-1);
		if (last?.literal === literal) last.text += text;
		else word.push({ text, literal });
		started = true;
	};
	const endWord = () => {
		if (started) words.push(word);
		word = [];
		started = false;
	};
	const endCommand = () => {
		endWord();
		if (words.length > 0) commands.push(words);
		words = [];
	};
	for (let i = 0; i < script.length; i++) {
		const c = script[i];
		if (quote === "'") {
			if (c === "'") quote = undefined;
			else push(c, true);
		} else if (c === "\\" && i + 1 < script.length && (quote === undefined || '$`"\\'.includes(script[i + 1]))) {
			push(script[++i], true);
		} else if (quote === '"') {
			if (c === '"') quote = undefined;
			else push(c, false);
		} else if (c === "'" || c === '"') {
			quote = c;
			started = true;
		} else if (c === "#" && !started) {
			while (i + 1 < script.length && script[i + 1] !== "\n") i++;
		} else if (c === ";" || c === "&" || c === "|" || c === "\n") endCommand();
		else if (/\s/.test(c)) endWord();
		else push(c, false);
	}
	endCommand();
	return commands;
}

const VARIABLE = /\$\{(\w+)\}|\$(\w+)/g;

/** A word with its variables replaced, or undefined when it holds a substitution or a variable nothing set. */
function expand(word: Word, vars: ReadonlyMap<string, string>): string | undefined {
	let out = "";
	for (const part of word) {
		if (part.literal) {
			out += part.text;
		} else if (part.text.includes("`") || part.text.includes("$(")) {
			return undefined;
		} else {
			out += part.text.replace(
				VARIABLE,
				(match, braced?: string, bare?: string) => vars.get(braced ?? bare ?? "") ?? match,
			);
		}
	}
	return out.includes("$") ? undefined : out;
}

const ASSIGNMENT = /^([A-Za-z_]\w*)=/;

function assignmentOf(word: Word): { name: string; value: Word } | undefined {
	const [first, ...rest] = word;
	const match = first && !first.literal ? ASSIGNMENT.exec(first.text) : null;
	if (!first || !match) return undefined;
	return { name: match[1], value: [{ ...first, text: first.text.slice(match[0].length) }, ...rest] };
}

// A heredoc body is data for another program (a script with its own `open(...)` calls), not shell.
const HEREDOC = /(?<!<)<<-?\s*(["']?)([A-Za-z_]\w*)\1/;

function withoutHeredocs(command: string): string {
	const kept: string[] = [];
	let end: string | undefined;
	for (const line of command.replace(/\\\n/g, " ").split("\n")) {
		if (end !== undefined) {
			if (line.trim() === end) end = undefined;
			continue;
		}
		kept.push(line);
		end = HEREDOC.exec(line)?.[2];
	}
	return kept.join("\n");
}

const OPENERS = new Set(["open", "/usr/bin/open"]);
const OPEN_FLAGS_WITH_VALUE = new Set(["-a", "-b", "--env", "--stdin", "--stdout", "--stderr"]);
const LEADING_KEYWORDS = new Set(["then", "do", "else"]);
const HAS_GLOB = /[*?[]/;
const isAnchored = (path: string) => path.startsWith("/") || path === "~" || path.startsWith("~/");

// The directory a `cd` leaves relative paths in: a path, null when unknown, undefined before any cd.
function nextCwd(cwd: string | null | undefined, dir: string | undefined): string | null {
	if (dir === undefined || dir === "" || dir === "-" || dir.split("/").includes("..")) return null;
	if (isAnchored(dir)) return dir.replace(/\/+$/, "");
	return typeof cwd === "string" ? `${cwd}/${dir.replace(/^\.\//, "").replace(/\/+$/, "")}` : null;
}

/**
 * The showable files a Bash command opens with `open`, best effort. It follows the variables a command assigns
 * and the directory it `cd`s to, and drops what it cannot resolve: a path with an unset variable, a glob or a URL.
 */
export function openedPaths(command: string): string[] {
	const found = new Set<string>();
	const vars = new Map([["HOME", "~"]]);
	let cwd: string | null | undefined;
	for (const words of simpleCommands(withoutHeredocs(command))) {
		let at = 0;
		while (at < words.length && LEADING_KEYWORDS.has(expand(words[at], vars) ?? "")) at++;
		if (expand(words[at] ?? [], vars) === "export") at++;
		for (let assignment = assignmentOf(words[at] ?? []); assignment; assignment = assignmentOf(words[at] ?? [])) {
			const value = expand(assignment.value, vars);
			if (value === undefined) vars.delete(assignment.name);
			else vars.set(assignment.name, value);
			at++;
		}
		const [head, ...args] = words.slice(at);
		if (!head) continue;
		const name = expand(head, vars) ?? "";
		if (name === "cd") {
			cwd = nextCwd(cwd, args.length === 1 ? expand(args[0], vars) : undefined);
			continue;
		}
		if (!OPENERS.has(name)) continue;
		let skip = false;
		for (const word of args) {
			const arg = expand(word, vars);
			if (skip) skip = false;
			else if (arg === "--args") break;
			else if (arg?.startsWith("-")) skip = OPEN_FLAGS_WITH_VALUE.has(arg);
			else if (arg !== undefined && isShowablePath(arg) && !HAS_GLOB.test(arg)) {
				if (isAnchored(arg) || cwd === undefined) addRef(found, arg);
				else if (cwd !== null) addRef(found, `${cwd}/${arg.replace(/^\.\//, "")}`);
			}
		}
	}
	return [...found];
}

const bashCommand = (call: ToolCallItem): string | undefined =>
	call.parentToolUseId === "" && call.name === "Bash" && isObject(call.input) && typeof call.input.command === "string"
		? call.input.command
		: undefined;

/**
 * What a turn showed the human, as refs for the daemon to confirm: the files a top-level Write or Edit changed, the
 * files a top-level Bash `open` opened, and the documents, images and videos a top-level reply names. A Read is
 * never a source, and a subagent's calls stay its own.
 */
export function docsOfTurn(items: readonly ChatItem[]): string[] {
	const found = new Set<string>();
	for (const item of items) {
		if (item.kind === "tool_call") {
			const path = writtenPath(item);
			if (path !== undefined && isShowablePath(path)) addRef(found, path);
			const command = bashCommand(item);
			if (command !== undefined) openedPaths(command).forEach((ref) => addRef(found, ref));
		} else if (item.kind === "assistant_text" && item.parentToolUseId === "") {
			for (const ref of [...docRefs(item.text), ...mediaRefs(item.text)]) addRef(found, ref);
		}
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
const ROW_KINDS: ReadonlySet<DocKind> = new Set(["markdown", "html", "pdf", "image", "video"]);

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

const KIND_TAG: Record<Exclude<DocKind, "image" | "video">, string> = {
	markdown: "MD",
	html: "HTML",
	pdf: "PDF",
	json: "JSON",
	text: "TXT",
	log: "LOG",
	diff: "DIFF",
};

function extensionTag(path: string): string {
	const extension = path.split(".").at(-1)?.toUpperCase() ?? "";
	return extension === "JPEG" ? "JPG" : extension;
}

export function docTag(doc: Pick<FileDoc, "kind" | "path">): string {
	if (doc.kind === "image" || doc.kind === "video") return extensionTag(doc.path);
	return KIND_TAG[doc.kind];
}

export function sizeText(bytes: number): string {
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
	else if (doc.kind === "html") language = languageOf(doc.path);
	const all = splitLines(body);
	const lines = all.slice(0, limit).map((line, i) => ({ kind: "context" as const, num: i + 1, text: line }));
	return { path: doc.path, language, lines, hidden: all.length - lines.length };
}
