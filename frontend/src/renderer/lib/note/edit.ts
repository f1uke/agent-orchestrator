/**
 * Where a rendered block came from in the note's bytes.
 *
 * 🗝 THE RULE THIS MODULE EXISTS TO ENFORCE: a note is never regenerated from
 * what was drawn. The DOM is not serialised back to markdown and the document
 * is never rewritten. An edit replaces ONE known byte range and leaves every
 * other byte — frontmatter, code fences, tables, raw HTML, indentation,
 * trailing whitespace, line endings — exactly as the author left them.
 *
 * These are somebody's personal notes with no backup, so the mapping is not
 * allowed to be approximate. A construct whose bytes cannot be located with
 * certainty is reported as NOT EDITABLE and rendered read-only; there is no
 * best-effort path. `marked` makes this tractable by carrying each token's
 * `raw`, but `raw` alone is not enough:
 *
 *   - A NESTED list's `raw` is DE-INDENTED. `'  - [ ] a'` in the file arrives
 *     as `'- [ ] a'`, so a plain `indexOf` finds it in the wrong place or not
 *     at all.
 *   - A list ITEM's children are lexed from `item.text`, which has already lost
 *     the bullet and the task marker.
 *
 * So each level of the tree carries a "space": the string its children's `raw`
 * values are offsets into, plus a function mapping an offset in that string
 * back to an offset in the file. Descending into a list item builds a new space
 * by ALIGNING the item's text against its own source slice line by line, and
 * refuses (returns null, so the subtree is read-only) unless every line differs
 * only by leading whitespace — or, on the first line, by the bullet itself.
 * Blockquote content is refused by that same check, because `> ` is not
 * whitespace, which is exactly the honest answer for it.
 *
 * 🗝 A note containing a CR is read-only in full. `marked` rewrites `\r\n` to
 * `\n` before it lexes, so every `raw` in a CRLF note describes bytes that are
 * not the ones on disk — offsets drift by one per line, and a save would
 * silently convert the whole file's line endings. Rather than carry a second
 * coordinate translation through every mapping for a case a macOS vault never
 * produces, such a note is reported as not editable.
 */

import type { Token, Tokens } from "./parse";

/** A half-open byte range in the note's full content. */
export type Span = { start: number; end: number };

/**
 * A block the reader may type into, and the bytes it owns.
 *
 * The invariant, asserted before every write:
 * `content.slice(start, end) === prefix + text + suffix`.
 *
 * `prefix` is the block's own markup (a bullet, a task marker, `## `) and
 * `suffix` its trailing newlines. Only `text` is ever replaced, so a heading
 * stays a heading and an indented task item keeps its indentation and its
 * checkbox without the editor having to reconstruct any of it.
 */
export type EditableBlock = {
	start: number;
	end: number;
	prefix: string;
	text: string;
	suffix: string;
	/** Whether a newline typed into this block would still mean what it says. */
	multiline: boolean;
};

/** The `[ ]`/`[x]` of one task item: a single byte the reader can flip. */
export type TaskMarker = {
	/** Offset of the character BETWEEN the brackets. */
	offset: number;
	checked: boolean;
};

/** What `indexNote` knows about one parsed note. */
export type NoteIndex = {
	/** Every token whose bytes were located, mapped to its range. */
	spans: WeakMap<object, Span>;
	/** The subset that can be safely rewritten in place. */
	editable: WeakMap<object, EditableBlock>;
};

type Space = {
	/** The string this level's `raw` values are offsets into. */
	text: string;
	/** That string's offsets, in the note's own coordinates. */
	toSource: (offset: number) => number;
};

/** A list bullet or ordered marker, with the trailing space that follows it. */
const BULLET = /^[ \t]*(?:[-*+]|\d+[.)])[ \t]+$/;
/** The bullet plus a task marker, as it appears at the head of an item. */
const ITEM_HEAD = /^([ \t]*(?:[-*+]|\d+[.)])[ \t]+)(\[[ xX]\][ \t]+)?/;
/** An ATX heading's opening hashes. */
const HEADING_HEAD = /^([ \t]{0,3}#{1,6}[ \t]+)/;
const BLANK = /^[ \t]*$/;

/**
 * Locates every block of a parsed note in the file's bytes.
 *
 * `source` is the text that was PARSED, which is the file minus its frontmatter
 * and minus the leading heading the page draws as the title. `base` is where
 * that text starts in the file — both of those strips are prefix-only, so the
 * offset is the length difference and nothing has to be re-derived.
 */
export function indexNote(source: string, tokens: Token[], base = 0): NoteIndex {
	const index: NoteIndex = { spans: new WeakMap(), editable: new WeakMap() };
	// See the module note: `marked` normalises CRLF away before lexing, so in
	// such a note no `raw` describes the bytes on disk. Nothing is mapped, which
	// renders the whole note read-only rather than risk rewriting its endings.
	if (source.includes("\r")) return index;
	walk(tokens, { text: source, toSource: (offset) => offset + base }, index, 0, true);
	return index;
}

/**
 * Walks one level of the tree, in order, consuming the space's text as it goes.
 *
 * A token whose `raw` cannot be found from the cursor means this level no
 * longer lines up with the file, so the walk STOPS rather than guessing: a span
 * that is merely plausible is the one thing this module must never produce.
 */
function walk(tokens: Token[], space: Space, index: NoteIndex, from: number, atRoot: boolean, lead = ""): void {
	let cursor = from;
	for (const token of tokens) {
		const raw = (token as { raw?: string }).raw ?? "";
		if (raw === "") continue;
		const at = space.text.startsWith(raw, cursor) ? cursor : space.text.indexOf(raw, cursor);
		if (at < 0) return;
		const end = at + raw.length;
		cursor = end;
		const span = { start: space.toSource(at), end: space.toSource(end) };
		index.spans.set(token, span);

		switch (token.type) {
			case "list":
				// An item's `raw` is a substring of its list's, so items share the
				// list's space rather than getting one of their own.
				walk((token as Tokens.List).items as unknown as Token[], space, index, at, atRoot);
				break;
			case "list_item": {
				const item = token as Tokens.ListItem;
				const inner = itemSpace(space, at, end, item);
				// A LOOSE task item's paragraph `raw` still carries the "[x] "
				// marker, so it is handed down as the lead: the first block inside
				// the item keeps it as untouchable prefix rather than offering it as
				// text. (A tight item's contents were lexed without it, so its own
				// first block simply starts after it.)
				if (inner) walk(item.tokens, inner.space, index, 0, false, inner.marker);
				break;
			}
			case "heading": {
				const block = headingBlock(space, span, at, end, at === from ? lead : "");
				if (block) index.editable.set(token, block);
				break;
			}
			case "paragraph":
			case "text": {
				const block = proseBlock(space, span, at, end, atRoot, at === from ? lead : "");
				if (block) index.editable.set(token, block);
				break;
			}
			default:
				// code, table, html, blockquote, hr, space: located but never edited.
				break;
		}
	}
}

/**
 * The coordinate space of a list item's contents.
 *
 * `item.text` has lost the bullet, and for a task item `marked` also lexed the
 * contents WITH the `[x] ` marker still attached (a loose item's paragraph
 * `raw` proves it), so the marker is read back out of the source and put in
 * front. The alignment below then has to account for the bullet on the first
 * line and the indentation on every other one — and refuses outright if any
 * line differs by anything else, which is what keeps a blockquote's `> ` from
 * being mistaken for indentation.
 */
function itemSpace(
	parent: Space,
	at: number,
	end: number,
	item: Tokens.ListItem,
): { space: Space; marker: string } | null {
	const outer = parent.text.slice(at, end);
	const head = ITEM_HEAD.exec(outer);
	const marker = item.task && head?.[2] ? head[2] : "";
	const space = alignLines(parent, at, outer, marker + item.text);
	return space ? { space, marker } : null;
}

/**
 * Maps `inner`'s offsets onto the file, given that `outer` is what `inner`
 * looks like in the file.
 *
 * The two must have the same lines in the same order, each outer line being its
 * inner line with something in FRONT of it: whitespace, or on the first line a
 * bullet. Anything else — a `>` quote marker, a reflowed line, a count
 * mismatch — returns null, and the caller renders that subtree read-only.
 */
function alignLines(parent: Space, at: number, outer: string, inner: string): Space | null {
	const innerLines = inner.split("\n");
	const outerLines = outer.split("\n");
	if (outerLines.length < innerLines.length) return null;

	// Where each inner line's first character sits inside `outer`.
	const starts: number[] = [];
	let offset = 0;
	for (let i = 0; i < innerLines.length; i += 1) {
		const outerLine = outerLines[i];
		const innerLine = innerLines[i];
		if (!outerLine.endsWith(innerLine)) return null;
		const prefix = outerLine.slice(0, outerLine.length - innerLine.length);
		if (!(BLANK.test(prefix) || (i === 0 && BULLET.test(prefix)))) return null;
		starts.push(offset + prefix.length);
		offset += outerLine.length + 1;
	}

	return {
		text: inner,
		toSource: (target) => {
			let remaining = target;
			for (let i = 0; i < innerLines.length; i += 1) {
				if (remaining <= innerLines[i].length) return parent.toSource(at + starts[i] + remaining);
				remaining -= innerLines[i].length + 1;
			}
			return parent.toSource(at + outer.length);
		},
	};
}

/**
 * A heading, split into its hashes and its words.
 *
 * A setext heading (underlined with `===`) and a closed one (`## Title ##`)
 * are refused: rewriting either needs the editor to reconstruct punctuation it
 * did not show the reader, and a heading is not worth guessing at.
 */
function headingBlock(space: Space, span: Span, at: number, end: number, lead: string): EditableBlock | null {
	const whole = space.text.slice(at, end);
	const carried = lead !== "" && whole.startsWith(lead) ? lead : "";
	const slice = whole.slice(carried.length);
	const head = HEADING_HEAD.exec(slice);
	if (!head) return null;
	const rest = slice.slice(head[0].length);
	const body = rest.replace(/(\r?\n)*$/, "");
	if (body.includes("\n") || /(^|[ \t])#+[ \t]*$/.test(body)) return null;
	const trailing = /[ \t]*$/.exec(body)?.[0] ?? "";
	const text = body.slice(0, body.length - trailing.length);
	return owning(span.start, carried + head[0], text, trailing + rest.slice(body.length), false);
}

/**
 * A paragraph, or a list item's own line of prose.
 *
 * Inside a list item only a SINGLE line is offered. An item's text wrapping
 * onto a second source line would have to be rewritten with the item's own
 * indentation on every continuation, and getting that wrong silently ends the
 * list — so it is left read-only instead. A top-level paragraph has no
 * indentation to reproduce and may be edited across lines.
 */
function proseBlock(
	space: Space,
	span: Span,
	at: number,
	end: number,
	atRoot: boolean,
	lead: string,
): EditableBlock | null {
	const whole = space.text.slice(at, end);
	const carried = lead !== "" && whole.startsWith(lead) ? lead : "";
	const slice = whole.slice(carried.length);
	const text = slice.replace(/(\r?\n)*$/, "");
	const suffix = slice.slice(text.length);
	if (!atRoot && text.includes("\n")) return null;
	return owning(span.start, carried, text, suffix, atRoot);
}

/**
 * A block that owns exactly the bytes it is made of.
 *
 * The END is derived from those bytes rather than from the token's mapped end,
 * because a `raw` that finishes with a newline maps to the START OF THE NEXT
 * LINE'S TEXT — which, under an indented list item, is past that line's
 * indentation. Those two bytes belong to the next line, not to this block, and
 * a range that claimed them would splice them away.
 */
function owning(start: number, prefix: string, text: string, suffix: string, multiline: boolean): EditableBlock {
	return { start, end: start + prefix.length + text.length + suffix.length, prefix, text, suffix, multiline };
}

/**
 * The task marker of a list item, given the item's own byte range.
 *
 * Ticking a box rewrites ONE character, which is as surgical as a write to
 * somebody's notes can be.
 */
export function taskMarker(content: string, span: Span): TaskMarker | null {
	const match = /^([ \t]*(?:[-*+]|\d+[.)])[ \t]+)\[([ xX])\]/.exec(content.slice(span.start, span.end));
	if (!match) return null;
	return { offset: span.start + match[1].length + 1, checked: match[2] !== " " };
}

/**
 * The note with one task marker flipped, and every other byte untouched.
 *
 * Throws rather than writing if the byte is not the one that was mapped — the
 * note moved under the reader, and a save is not the place to find out by
 * overwriting something else.
 */
export function toggleTask(content: string, marker: TaskMarker): string {
	const current = content.slice(marker.offset, marker.offset + 1);
	if (current !== " " && current !== "x" && current !== "X") {
		throw new Error("Refusing to write: this checkbox is no longer where it was read.");
	}
	const next = marker.checked ? " " : "x";
	return content.slice(0, marker.offset) + next + content.slice(marker.offset + 1);
}

/**
 * The note with one block's text replaced, and every other byte untouched.
 *
 * The block's own bytes are re-checked against the note first. That is not
 * belt-and-braces: it is the difference between splicing the block that was
 * mapped and splicing whatever now happens to sit at those offsets.
 */
export function spliceBlock(content: string, block: EditableBlock, text: string): string {
	const owned = block.prefix + block.text + block.suffix;
	if (content.slice(block.start, block.end) !== owned) {
		throw new Error("Refusing to write: this block is no longer where it was read.");
	}
	const written = block.multiline ? text : text.replace(/\r?\n[ \t]*/g, " ");
	return content.slice(0, block.start) + block.prefix + written + block.suffix + content.slice(block.end);
}
