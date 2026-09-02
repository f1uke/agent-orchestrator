/**
 * Parsing a vault note into something the renderer can draw.
 *
 * 🗝 Vault content is UNTRUSTED INPUT. It is markdown off somebody's disk,
 * possibly written by an agent, and it is rendered inside the app's own origin.
 * Two rules follow, and both are enforced here rather than left to the caller:
 *
 *   1. No HTML ever reaches the DOM. `marked` is used as a LEXER only — this
 *      module hands back a token tree, and `NoteMarkdown` turns tokens into
 *      React elements. Nothing is stringified into `innerHTML`, so a `<script>`
 *      or an `onerror=` attribute in a note is inert by construction rather
 *      than by a sanitiser we would have to keep right.
 *   2. A link cannot navigate the app away. `safeHref` allows only http(s) and
 *      mailto, so `javascript:` and `file:` URLs come back as plain text.
 *
 * `marked` was chosen over remark/rehype for exactly this: it is one
 * zero-dependency package that exposes a nested AST, where the unified stack
 * would have added a chain of packages whose default output is an HTML string
 * that then needs sanitising.
 */

import { Marked, type Token, type Tokens, type TokenizerExtension } from "marked";

/** A `[[wikilink]]`, optionally aliased (`[[target|label]]`) and/or anchored. */
export type WikilinkToken = {
	type: "wikilink";
	raw: string;
	/** The note being linked to, without any alias or `#heading` anchor. */
	target: string;
	/** The `#heading` part, without its hash. Empty when the link has none. */
	anchor: string;
	/** What to draw. The alias when there is one, else the target. */
	label: string;
	/**
	 * Whether the link carried a `|alias`. Both spellings render as `label`
	 * alone — the brackets are syntax — so this exists for a writer that puts a
	 * link BACK into the source, which has to know which form to restore.
	 */
	aliased: boolean;
};

/**
 * `[[target|label]]`, `[[target#anchor]]`, `[[target]]`.
 *
 * A wikilink is an extension rather than a text-splitting pass because it has
 * an unambiguous opening delimiter: there is no preceding-character rule to
 * apply, which is exactly what makes `#tags` (below) the other way round.
 */
const wikilink: TokenizerExtension = {
	name: "wikilink",
	level: "inline",
	start(src: string) {
		const i = src.indexOf("[[");
		return i < 0 ? undefined : i;
	},
	tokenizer(src: string) {
		const match = /^\[\[([^\]|\n]+?)(?:\|([^\]\n]*))?\]\]/.exec(src);
		if (!match) return undefined;
		const [rawTarget, rawAnchor = ""] = match[1].split("#", 2);
		const target = rawTarget.trim();
		const alias = (match[2] ?? "").trim();
		return {
			type: "wikilink",
			raw: match[0],
			target,
			anchor: rawAnchor.trim(),
			label: alias || match[1].trim(),
			aliased: alias !== "",
		} satisfies WikilinkToken;
	},
};

// One instance, not the `marked` singleton: configuring the singleton would
// change every other caller's parser, and this one deliberately differs.
const lexer = new Marked({
	// Off by design (see the module note). With `false`, raw HTML in a note is
	// carried as an inert `html` token that the renderer draws as literal text.
	async: false,
	gfm: true,
	// A single newline is a line break inside a paragraph — how notes are
	// actually written by hand, and how Obsidian renders them.
	breaks: true,
});
lexer.use({ extensions: [wikilink] });

/** A note, split into the parts the reader sees separately. */
export type ParsedNote = {
	/** Tags from YAML frontmatter, shown as pills under the title. */
	frontmatterTags: string[];
	/** The frontmatter's `title:`, when it has one. */
	frontmatterTitle: string;
	/** The body's token tree, frontmatter removed. */
	tokens: Token[];
	/** Words in the body, for the "· 340 words" caption. */
	wordCount: number;
};

/**
 * Splits YAML frontmatter off the head of a note.
 *
 * Deliberately not a YAML parse: only `title` and `tags` are read, and pulling
 * in a YAML parser to read two keys out of untrusted text would be a much
 * larger surface than the feature needs. Anything else in the block is dropped
 * rather than half-understood.
 */
function splitFrontmatter(source: string): { body: string; tags: string[]; title: string } {
	const match = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?/.exec(source);
	if (!match) return { body: source, tags: [], title: "" };

	const tags: string[] = [];
	let title = "";
	let inTagList = false;
	for (const line of match[1].split(/\r?\n/)) {
		const listItem = /^\s*-\s*(.+?)\s*$/.exec(line);
		if (inTagList && listItem) {
			tags.push(stripQuotes(listItem[1]));
			continue;
		}
		inTagList = false;
		const pair = /^([A-Za-z_][\w-]*)\s*:\s*(.*)$/.exec(line);
		if (!pair) continue;
		const [, key, rawValue] = pair;
		const value = rawValue.trim();
		if (key.toLowerCase() === "title") {
			title = stripQuotes(value);
			continue;
		}
		if (key.toLowerCase() !== "tags") continue;
		if (value === "") {
			// `tags:` followed by a `- one` list on the lines beneath.
			inTagList = true;
			continue;
		}
		// `tags: [a, b]` or `tags: a, b`.
		for (const part of value.replace(/^\[|\]$/g, "").split(",")) {
			const tag = stripQuotes(part.trim());
			if (tag) tags.push(tag);
		}
	}
	return { body: source.slice(match[0].length), tags, title };
}

function stripQuotes(value: string): string {
	return value.replace(/^["']|["']$/g, "").trim();
}

/** Parses one note. Never throws: an unparseable note renders as its own text. */
export function parseNote(source: string): ParsedNote {
	const { body, tags, title } = splitFrontmatter(source);
	let tokens: Token[];
	try {
		tokens = lexer.lexer(body);
	} catch {
		tokens = [{ type: "paragraph", raw: body, text: body, tokens: [{ type: "text", raw: body, text: body }] } as Token];
	}
	return {
		frontmatterTags: tags,
		frontmatterTitle: title,
		tokens,
		wordCount: countWords(body),
	};
}

/**
 * A word count that behaves on Thai as well as on English. Thai is written
 * without spaces, so counting whitespace-delimited runs reports a paragraph of
 * Thai as one word; each run of Thai characters is instead counted at roughly
 * five characters per word, which is the usual reading-length approximation.
 */
export function countWords(text: string): number {
	const spaced = text.match(/[^\s฀-๿]+/g)?.length ?? 0;
	const thaiChars = text.match(/[฀-๿]/g)?.length ?? 0;
	return spaced + Math.ceil(thaiChars / 5);
}

/** One `#tag`, or the plain text around it. */
export type InlineSegment = { kind: "text"; text: string } | { kind: "tag"; tag: string };

// A tag must start the string or follow whitespace or an opening bracket, so
// `C#`, `issue#12` and a `#fragment` inside a URL are left alone. It is done by
// splitting text tokens rather than by a marked extension because an inline
// tokenizer is handed only the text FROM the "#" onwards, and so cannot see the
// character before it — the whole rule.
//
// 🗝 The continuation class includes \p{M} (combining marks). Without it a Thai
// tag is cut at its first vowel sign — `#ความรู้` came back as `#ความร` plus
// stray text — because Thai vowels and tone marks are marks, not letters.
const TAG_PATTERN = /(^|[\s(\[])#([\p{L}\p{N}][\p{L}\p{M}\p{N}_/-]*)/gu;

/** Splits a run of plain text into its `#tag` pills and the text between them. */
export function splitTags(text: string): InlineSegment[] {
	const segments: InlineSegment[] = [];
	let cursor = 0;
	TAG_PATTERN.lastIndex = 0;
	for (let match = TAG_PATTERN.exec(text); match !== null; match = TAG_PATTERN.exec(text)) {
		const lead = match[1];
		const start = match.index + lead.length;
		if (start > cursor) segments.push({ kind: "text", text: text.slice(cursor, start) });
		segments.push({ kind: "tag", tag: match[2] });
		cursor = match.index + match[0].length;
	}
	if (cursor < text.length) segments.push({ kind: "text", text: text.slice(cursor) });
	return segments.length > 0 ? segments : [{ kind: "text", text }];
}

/** The five Obsidian callout kinds this renderer draws distinctly. */
export type CalloutKind = "note" | "tip" | "warning" | "danger" | "quote";

export type Callout = {
	kind: CalloutKind;
	/** The callout's own title, or the capitalised kind when it has none. */
	title: string;
	/** The blockquote's content, with the `[!kind]` marker line removed. */
	tokens: Token[];
};

const CALLOUT_KINDS: Record<string, CalloutKind> = {
	note: "note",
	info: "note",
	abstract: "note",
	summary: "note",
	tip: "tip",
	hint: "tip",
	success: "tip",
	check: "tip",
	done: "tip",
	question: "tip",
	warning: "warning",
	caution: "warning",
	attention: "warning",
	danger: "danger",
	error: "danger",
	bug: "danger",
	failure: "danger",
	quote: "quote",
	cite: "quote",
};

/**
 * Reads an Obsidian callout out of a blockquote — `> [!note] Title`. Returns
 * null for an ordinary blockquote, which the renderer then draws as a quote.
 */
export function readCallout(quote: Tokens.Blockquote): Callout | null {
	const [first] = quote.tokens;
	if (!first || first.type !== "paragraph") return null;
	const paragraph = first as Tokens.Paragraph;
	const match = /^\[!([A-Za-z]+)\]([+-])?[ \t]*(.*)/.exec(paragraph.text);
	if (!match) return null;
	const kind = CALLOUT_KINDS[match[1].toLowerCase()];
	if (!kind) return null;

	// Drop the marker line; anything after it on the same paragraph is body.
	const [, , , titleAndRest] = match;
	const rest = paragraph.text.slice(match[0].length).replace(/^\r?\n/, "");
	const body: Token[] = [];
	if (rest.trim() !== "") body.push(...lexer.lexer(rest));
	body.push(...quote.tokens.slice(1));

	return {
		kind,
		title: titleAndRest.trim() || kind.charAt(0).toUpperCase() + kind.slice(1),
		tokens: body,
	};
}

/**
 * A link target the renderer is willing to draw as a link.
 *
 * Only http(s) and mailto survive. Everything else — `javascript:`, `file:`,
 * `data:`, a protocol-relative `//host` — comes back empty and is rendered as
 * text, so a note cannot navigate the renderer off the app or run anything.
 */
export function safeHref(href: string): string {
	const trimmed = href.trim();
	if (trimmed.startsWith("//")) return "";
	let url: URL;
	try {
		url = new URL(trimmed);
	} catch {
		return "";
	}
	return url.protocol === "http:" || url.protocol === "https:" || url.protocol === "mailto:" ? url.href : "";
}

export type { Token, Tokens };
