import { describe, expect, it } from "vitest";
import { indexNote, spliceBlock, taskMarker, toggleTask, type EditableBlock, type Span } from "./edit";
import { parseNote, type Token } from "./parse";

/**
 * Indexes a note the way the view does: parse the whole file (which strips the
 * frontmatter), then map the body's tokens back onto the file's own bytes.
 */
function indexed(content: string) {
	const { tokens } = parseNote(content);
	const body = content.slice(content.length - bodyLength(content));
	return { index: indexNote(body, tokens, content.length - body.length), tokens, content };
}

/** The parsed body's length, i.e. the file minus its frontmatter. */
function bodyLength(content: string): number {
	return content.replace(/^---\r?\n[\s\S]*?\r?\n---\r?\n?/, "").length;
}

/** Every token in document order, so a test can reach a nested one. */
function flatten(tokens: Token[]): Token[] {
	const out: Token[] = [];
	const visit = (list: Token[]) => {
		for (const token of list) {
			out.push(token);
			const nested = token as { tokens?: Token[]; items?: Token[] };
			if (nested.items) visit(nested.items);
			if (nested.tokens) visit(nested.tokens);
		}
	};
	visit(tokens);
	return out;
}

function blockWithText(content: string, text: string): { block: EditableBlock; content: string } {
	const { index, tokens } = indexed(content);
	for (const token of flatten(tokens)) {
		const block = index.editable.get(token);
		if (block?.text === text) return { block, content };
	}
	throw new Error(`no editable block reading ${JSON.stringify(text)}`);
}

function itemSpans(content: string): Span[] {
	const { index, tokens } = indexed(content);
	return flatten(tokens)
		.filter((token) => token.type === "list_item")
		.map((token) => index.spans.get(token))
		.filter((span): span is Span => span !== undefined);
}

/**
 * A block's range must name the exact bytes it claims. This is the invariant
 * every write depends on, so it is asserted directly rather than only through
 * the writes below.
 */
describe("block ranges", () => {
	const NOTE = [
		"---",
		"title: Tasks",
		"tags: [work]",
		"---",
		"",
		"# Heading one",
		"",
		"A plain paragraph.",
		"",
		"## Tasks",
		"",
		"- Parent",
		"  - [ ] child one",
		"  - [x] child two",
		"",
		"```js",
		"const a = 1;",
		"```",
		"",
		"| a | b |",
		"| - | - |",
		"| 1 | 2 |",
		"",
	].join("\n");

	it("owns exactly the bytes it names", () => {
		const { index, tokens, content } = indexed(NOTE);
		let checked = 0;
		for (const token of flatten(tokens)) {
			const block = index.editable.get(token);
			if (!block) continue;
			expect(content.slice(block.start, block.end)).toBe(block.prefix + block.text + block.suffix);
			checked += 1;
		}
		expect(checked).toBeGreaterThan(3);
	});

	it("finds a nested task item's text through the de-indented raw", () => {
		const { block, content } = blockWithText(NOTE, "child one");
		expect(content.slice(block.start, block.end)).toBe("child one");
	});

	it("splits a heading into its hashes and its words", () => {
		const { block } = blockWithText(NOTE, "Heading one");
		expect(block.prefix).toBe("# ");
		expect(block.multiline).toBe(false);
	});

	it("leaves code blocks and tables unmapped for editing", () => {
		const { index, tokens } = indexed(NOTE);
		for (const token of flatten(tokens)) {
			if (token.type === "code" || token.type === "table") expect(index.editable.get(token)).toBeUndefined();
		}
	});
});

describe("task markers", () => {
	it("finds the marker of a nested item", () => {
		const note = "## Tasks\n\n- Parent\n  - [ ] child one\n  - [x] child two\n";
		const markers = itemSpans(note).map((span) => taskMarker(note, span));
		const found = markers.filter((m) => m !== null);
		expect(found).toHaveLength(2);
		expect(note[found[0]!.offset]).toBe(" ");
		expect(note[found[1]!.offset]).toBe("x");
		expect(found.map((m) => m!.checked)).toEqual([false, true]);
	});

	it("does not find one on a plain item that merely opens with a bracket", () => {
		const note = "- [note] something\n";
		expect(itemSpans(note).map((span) => taskMarker(note, span))).toEqual([null]);
	});

	it("flips one byte and leaves the rest of the note identical", () => {
		const note =
			"---\ntitle: T\n---\n\n## Tasks\n\n- Parent\n  - [ ] child one\n  - [x] child two\n\nA line with trailing space \n";
		const marker = itemSpans(note)
			.map((span) => taskMarker(note, span))
			.find((m) => m?.checked === false)!;
		const after = toggleTask(note, marker);
		expect(after).toHaveLength(note.length);
		expect(after.slice(0, marker.offset)).toBe(note.slice(0, marker.offset));
		expect(after.slice(marker.offset + 1)).toBe(note.slice(marker.offset + 1));
		expect(after[marker.offset]).toBe("x");
		// And back again.
		expect(toggleTask(after, { offset: marker.offset, checked: true })).toBe(note);
	});

	// The one byte a tick is allowed to change is the marker itself, and it is
	// always written lower case — which is what Obsidian writes, and the only
	// spelling a reader who has just clicked can be said to have chosen.
	it("reads an upper-case [X] as done and writes back a lower-case [x]", () => {
		const note = "- [X] done\n";
		const marker = taskMarker(note, itemSpans(note)[0])!;
		expect(marker.checked).toBe(true);
		const off = toggleTask(note, marker);
		expect(off).toBe("- [ ] done\n");
		expect(toggleTask(off, { offset: marker.offset, checked: false })).toBe("- [x] done\n");
	});

	it("refuses to write when the byte is no longer a checkbox", () => {
		const note = "- [ ] one\n";
		const marker = taskMarker(note, itemSpans(note)[0])!;
		expect(() => toggleTask("something else entirely\n", marker)).toThrow(/no longer/);
	});
});

/**
 * The whole promise of this feature: editing one block touches that block and
 * nothing else. Every case below asserts the file byte-for-byte, not just the
 * part that changed.
 */
describe("byte preservation", () => {
	const REAL_NOTE = [
		"---",
		"title: MOBILITY-4713-Webview-Zoom - Tasks",
		"type: tasks",
		"updated: 2026-09-02",
		"---",
		"",
		"# MOBILITY-4713-Webview-Zoom - Tasks",
		"",
		"Some prose with **bold** and a [[wikilink]] in it.",
		"",
		"## Tasks",
		"",
		"- Investigate",
		"  - [ ] reproduce on device",
		"  - [x] read the webview docs",
		"",
		"```ts",
		"const zoom = 1.0;   // trailing spaces below   ",
		"```",
		"",
		"| col | col |",
		"| --- | --- |",
		"|  a  |  b  |",
		"",
		"<div>raw html</div>",
		"",
		"\tan indented line\t",
		"",
	].join("\n");

	it("edits one paragraph and leaves every other byte alone", () => {
		const { block } = blockWithText(REAL_NOTE, "Some prose with **bold** and a [[wikilink]] in it.");
		const after = spliceBlock(REAL_NOTE, block, "Rewritten prose with **bold** still in it.");

		expect(after.slice(0, block.start)).toBe(REAL_NOTE.slice(0, block.start));
		expect(after.slice(block.start + "Rewritten prose with **bold** still in it.".length + block.suffix.length)).toBe(
			REAL_NOTE.slice(block.end),
		);
		// Frontmatter, code fence, table, raw HTML and the tab-indented line all
		// survive untouched.
		expect(after).toContain("---\ntitle: MOBILITY-4713-Webview-Zoom - Tasks\ntype: tasks\n");
		expect(after).toContain("const zoom = 1.0;   // trailing spaces below   ");
		expect(after).toContain("|  a  |  b  |");
		expect(after).toContain("<div>raw html</div>");
		expect(after).toContain("\tan indented line\t");
		// The file still ends the way it did.
		expect(after.endsWith("\n")).toBe(true);
		expect((after.match(/\n/g) ?? []).length).toBe((REAL_NOTE.match(/\n/g) ?? []).length);
	});

	it("edits a nested task item's text without disturbing its indentation or its box", () => {
		const { block } = blockWithText(REAL_NOTE, "reproduce on device");
		const after = spliceBlock(REAL_NOTE, block, "reproduce on a real device");
		expect(after).toContain("  - [ ] reproduce on a real device\n");
		expect(after).toContain("  - [x] read the webview docs\n");
		expect(after.length).toBe(REAL_NOTE.length + "reproduce on a real device".length - "reproduce on device".length);
	});

	it("edits a heading and keeps its level", () => {
		const { block } = blockWithText(REAL_NOTE, "Tasks");
		const after = spliceBlock(REAL_NOTE, block, "Open questions");
		expect(after).toContain("## Open questions\n");
		expect(after).not.toContain("## Tasks");
	});

	it("never lets a newline break a single-line block", () => {
		const { block } = blockWithText(REAL_NOTE, "reproduce on device");
		const after = spliceBlock(REAL_NOTE, block, "line one\n  line two");
		expect(after).toContain("  - [ ] line one line two\n");
	});

	it("keeps a newline the reader typed into a paragraph", () => {
		const { block } = blockWithText(REAL_NOTE, "Some prose with **bold** and a [[wikilink]] in it.");
		const after = spliceBlock(REAL_NOTE, block, "first line\nsecond line");
		expect(after).toContain("first line\nsecond line");
	});

	it("refuses to splice a block whose bytes have moved", () => {
		const { block } = blockWithText(REAL_NOTE, "reproduce on device");
		const drifted = REAL_NOTE.replace("# MOBILITY", "# Renamed MOBILITY");
		expect(() => spliceBlock(drifted, block, "anything")).toThrow(/no longer/);
	});

	it("survives a note with no frontmatter at all", () => {
		const note = "Just prose.\n\n- [ ] and a task\n";
		const { block } = blockWithText(note, "Just prose.");
		expect(spliceBlock(note, block, "Other prose.")).toBe("Other prose.\n\n- [ ] and a task\n");
	});
});

/**
 * A construct whose bytes cannot be located with certainty is offered
 * read-only. These pin the exclusions, so widening them later is a deliberate
 * decision rather than an accident.
 */
describe("what is deliberately read-only", () => {
	function editableTexts(content: string): string[] {
		const { index, tokens } = indexed(content);
		return flatten(tokens)
			.map((token) => index.editable.get(token)?.text)
			.filter((text): text is string => text !== undefined);
	}

	it("offers nothing inside a blockquote", () => {
		expect(editableTexts("> quoted line\n> more quoted\n")).toEqual([]);
	});

	it("offers nothing inside a callout", () => {
		expect(editableTexts("> [!note] Title\n> body text\n")).toEqual([]);
	});

	it("offers nothing for a setext heading", () => {
		expect(editableTexts("Underlined\n==========\n")).toEqual([]);
	});

	it("offers nothing for a closed ATX heading", () => {
		expect(editableTexts("## Closed ##\n")).toEqual([]);
	});

	it("offers nothing for a table row or a fenced block", () => {
		expect(editableTexts("| a |\n| - |\n| 1 |\n")).toEqual([]);
		expect(editableTexts("```\ncode\n```\n")).toEqual([]);
		expect(editableTexts("<div>html</div>\n")).toEqual([]);
	});

	it("offers nothing at all in a note with CRLF line endings", () => {
		// marked normalises CRLF away before lexing, so no `raw` in such a note
		// describes the bytes on disk. Read-only beats rewriting the endings.
		expect(editableTexts("# Title\r\n\r\nA paragraph.\r\n")).toEqual([]);
		expect(editableTexts("# Title\n\nA paragraph.\n")).toEqual(["Title", "A paragraph."]);
	});

	it("offers nothing for a list item whose own text wraps onto a second line", () => {
		const wrapped = "- a line that keeps going\n  and wraps onto the next one\n";
		expect(editableTexts(wrapped)).toEqual([]);
	});

	it("still offers the plain paragraphs around all of that", () => {
		const mixed = "Before.\n\n> quoted\n\n```\ncode\n```\n\nAfter.\n";
		expect(editableTexts(mixed)).toEqual(["Before.", "After."]);
	});
});

describe("deep nesting", () => {
	it("maps a task three levels down onto its real bytes", () => {
		const note = "- a\n  - b\n    - [ ] deep task\n";
		const { block, content } = blockWithText(note, "deep task");
		expect(content.slice(block.start, block.end)).toBe("deep task");
		expect(spliceBlock(note, block, "deeper task")).toBe("- a\n  - b\n    - [ ] deeper task\n");

		const marker = itemSpans(note)
			.map((span) => taskMarker(note, span))
			.find((m) => m !== null)!;
		expect(toggleTask(note, marker)).toBe("- a\n  - b\n    - [x] deep task\n");
	});

	it("maps a loose task list, whose items carry the marker in their paragraph", () => {
		const note = "- [ ] one\n\n- [x] two\n";
		const { block } = blockWithText(note, "one");
		expect(spliceBlock(note, block, "uno")).toBe("- [ ] uno\n\n- [x] two\n");

		const markers = itemSpans(note).map((span) => taskMarker(note, span));
		expect(toggleTask(note, markers[1]!)).toBe("- [ ] one\n\n- [ ] two\n");
	});

	it("maps an ordered task list", () => {
		const note = "1. [ ] first\n2. [x] second\n";
		const { block } = blockWithText(note, "first");
		expect(spliceBlock(note, block, "first thing")).toBe("1. [ ] first thing\n2. [x] second\n");
	});

	it("keeps the right item when two read the same", () => {
		const note = "- [ ] same\n- [ ] same\n";
		const spans = itemSpans(note);
		const second = taskMarker(note, spans[1])!;
		expect(toggleTask(note, second)).toBe("- [ ] same\n- [x] same\n");
	});
});

describe("frontmatter is out of reach of a body edit", () => {
	it("offsets every block past the frontmatter", () => {
		const note = "---\ntitle: T\n---\n\nBody line.\n";
		const { block } = blockWithText(note, "Body line.");
		expect(block.start).toBe(note.indexOf("Body line."));
		expect(spliceBlock(note, block, "Other line.")).toBe("---\ntitle: T\n---\n\nOther line.\n");
	});

	it("is not confused by a horizontal rule that looks like frontmatter", () => {
		const note = "Body.\n\n---\n\nMore.\n";
		const { block } = blockWithText(note, "More.");
		expect(spliceBlock(note, block, "Even more.")).toBe("Body.\n\n---\n\nEven more.\n");
	});
});

/**
 * The strongest guarantee available without a fuzzer: over a corpus of notes
 * shaped like a real vault's, writing a block back UNCHANGED must return the
 * file byte-for-byte. Anything the mapping gets wrong — an off-by-one, a
 * swallowed indent, a lost newline — shows up here as a changed file.
 */
describe("writing a block back unchanged is a no-op", () => {
	const CORPUS = [
		"# Title\n\nOne paragraph.\n",
		"---\ntitle: T\ntags:\n  - a\n  - b\n---\n\n# T\n\nBody.\n",
		"## Tasks\n\n- Parent\n  - [ ] one\n  - [x] two\n    - [ ] deeper\n",
		"- [ ] a\n\n- [x] b\n\n- plain\n",
		"1. first\n2. [ ] second\n3. third\n",
		"Text\n\n> quoted\n\n```js\ncode();\n```\n\n| a | b |\n| - | - |\n| 1 | 2 |\n\nAfter.\n",
		"> [!warning] Careful\n> body\n\nAfter the callout.\n",
		"Para one\nsoft wrapped onto a second line.\n\nPara two.\n",
		"* star bullets\n* [ ] and a star task\n",
		"+ plus bullet\n\n  continued paragraph inside the item\n",
		"#### deep heading\n\n- [x] a done task\n",
		"Trailing whitespace follows:   \n\n- item   \n",
		"\u0e2b\u0e31\u0e27\u0e02\u0e49\u0e2d\n\n- [ ] \u0e07\u0e32\u0e19 #\u0e04\u0e27\u0e32\u0e21\u0e23\u0e39\u0e49\n",
		"No trailing newline at all",
		"",
	];

	for (const [n, note] of CORPUS.entries()) {
		it(`corpus note ${n}`, () => {
			const { index, tokens } = indexed(note);
			let seen = 0;
			for (const token of flatten(tokens)) {
				const block = index.editable.get(token);
				if (!block) continue;
				seen += 1;
				expect(note.slice(block.start, block.end)).toBe(block.prefix + block.text + block.suffix);
				expect(spliceBlock(note, block, block.text)).toBe(note);
			}
			// Ticking every box and unticking it again must also come back exactly.
			for (const span of itemSpans(note)) {
				const marker = taskMarker(note, span);
				if (!marker) continue;
				seen += 1;
				const flipped = toggleTask(note, marker);
				expect(flipped).toHaveLength(note.length);
				expect(toggleTask(flipped, { offset: marker.offset, checked: !marker.checked })).toBe(note);
			}
			expect(seen).toBeGreaterThanOrEqual(0);
		});
	}
});
