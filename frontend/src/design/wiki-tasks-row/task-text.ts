/**
 * The pure text passes the redesigned Tasks row needs.
 *
 * Both of these are RENDERER-ONLY on purpose. The daemon's `Task.Raw` — the
 * byte-exact line a tick is written to — is untouched by anything here, and
 * `Task.Text` keeps carrying the `(from: …)` tag exactly as the vault wrote it.
 * What changes is only where the reader sees it.
 *
 * 🗝 Vault text is UNTRUSTED. `splitWikilinks` returns TOKENS, never markup:
 * the caller turns them into React elements, so nothing here can reach the DOM
 * as HTML. Same rule as `lib/note/parse.ts`.
 */

/** `(from: …)`, the daemon's own pattern (backend `service/wiki/tasks.go`). */
const FROM_TAG = /\(\s*from:([^)\n]*)\)/gi;

/** An ISO day anywhere inside a from-tag: it is what dates the row already. */
const ISO_DAY = /\d{4}-\d{2}-\d{2}/g;

export type SplitText = {
	/** The sentence with every `(from: …)` lifted out of it. */
	text: string;
	/** Each tag's inside, trimmed, in the order they appeared. */
	tags: string[];
};

/**
 * Lifts every `(from: …)` out of a row's text.
 *
 * The tag is nearly always the tail of the sentence, so removing it leaves a
 * trailing space and sometimes a dangling separator; both are cleaned up so the
 * row reads as the sentence it was before somebody appended provenance to it.
 */
export function splitFromTags(raw: string): SplitText {
	const tags: string[] = [];
	const text = raw
		.replace(FROM_TAG, (_match, inside: string) => {
			const content = inside.trim();
			if (content !== "") tags.push(content);
			return "";
		})
		.replace(/[ \t]{2,}/g, " ")
		.replace(/\s+([,.;:])/g, "$1")
		.replace(/[\s ]*[—–\-·|]\s*$/u, "")
		.trim();
	return { text, tags };
}

/**
 * Whether a from-tag still says something the row does not already say.
 *
 * Two ways it can be redundant, and both are common in the vault:
 *
 *  1. It names the section the row already sits in — `(from: My active items)`
 *     directly above a source line reading `… · My active items`.
 *  2. Its only content is a date, which is exactly what put the row under its
 *     day-group header in the first place.
 */
export function fromTagAddsSomething(tag: string, section: string, subsection: string): boolean {
	const withoutDate = tag.replace(ISO_DAY, " ").replace(/\s+/g, " ").trim();
	if (withoutDate === "") return false;
	const needle = withoutDate.toLowerCase().replace(/[.,;:]+$/, "");
	return needle !== section.trim().toLowerCase() && needle !== subsection.trim().toLowerCase();
}

export type TextPart =
	{ kind: "text"; value: string } | { kind: "wikilink"; target: string; anchor: string; label: string };

/** `[[target]]`, `[[target|label]]`, `[[target#anchor]]` — the Notes tab's grammar. */
const WIKILINK = /\[\[([^\]|\n]+?)(?:\|([^\]\n]*))?\]\]/g;

/**
 * Splits a row into plain runs and wikilinks.
 *
 * The brackets are SYNTAX, not text — Obsidian and the Notes tab both draw
 * `[[a-note]]` as "a-note" — so the label carries no brackets and the caller
 * draws the pill instead.
 */
export function splitWikilinks(text: string): TextPart[] {
	const parts: TextPart[] = [];
	let cursor = 0;
	for (const match of text.matchAll(WIKILINK)) {
		const at = match.index ?? 0;
		if (at > cursor) parts.push({ kind: "text", value: text.slice(cursor, at) });
		const [rawTarget, rawAnchor = ""] = match[1].split("#", 2);
		const alias = (match[2] ?? "").trim();
		parts.push({
			kind: "wikilink",
			target: rawTarget.trim(),
			anchor: rawAnchor.trim(),
			label: alias || match[1].trim(),
		});
		cursor = at + match[0].length;
	}
	if (cursor < text.length) parts.push({ kind: "text", value: text.slice(cursor) });
	return parts.length > 0 ? parts : [{ kind: "text", value: text }];
}

/**
 * Where the row lives, short enough to stay quiet.
 *
 * `noteLabel` (shipped) draws `<folder>/<basename>` because a vault that keeps
 * one task note per project names them all the same thing. This goes one step
 * further and drops an UNDERSCORE-PREFIXED basename entirely: `_tasks` is a
 * container convention, not a title, so `mobile-development/_tasks` carries
 * exactly as much meaning as `mobile-development` and seven fewer characters of
 * a line the reader was told is not important. Any other basename is kept —
 * `frontier/roadmap` still distinguishes itself from `frontier/backlog`.
 *
 * The address is not lost: the full `path:line` is the button's title.
 */
export function sourceLabel(path: string): string {
	const segments = path.split("/");
	const base = (segments.pop() ?? path).replace(/\.md$/i, "");
	const folder = segments.pop() ?? "";
	if (base.startsWith("_")) return folder || base;
	return folder ? `${folder}/${base}` : base;
}
