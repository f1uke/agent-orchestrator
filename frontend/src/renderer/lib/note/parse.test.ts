import { describe, expect, it } from "vitest";
import { countWords, parseNote, readCallout, safeHref, splitTags, type Tokens } from "./parse";

describe("parseNote frontmatter", () => {
	it("lifts inline and list tags out of YAML frontmatter", () => {
		const inline = parseNote("---\ntags: [llm, agents]\ntitle: Compaction\n---\n\n# Body\n");
		expect(inline.frontmatterTags).toEqual(["llm", "agents"]);
		expect(inline.frontmatterTitle).toBe("Compaction");

		const list = parseNote("---\ntags:\n  - llm\n  - agents\n---\n\nbody\n");
		expect(list.frontmatterTags).toEqual(["llm", "agents"]);
	});

	it("leaves a note without frontmatter untouched", () => {
		const note = parseNote("# Title\n\nbody\n");
		expect(note.frontmatterTags).toEqual([]);
		expect((note.tokens[0] as Tokens.Heading).depth).toBe(1);
	});

	it("does not mistake a horizontal rule for frontmatter", () => {
		const note = parseNote("body\n\n---\n\nmore\n");
		expect(note.tokens.some((t) => t.type === "hr")).toBe(true);
	});
});

describe("wikilinks", () => {
	function inlineTokens(markdown: string) {
		const note = parseNote(markdown);
		return (note.tokens[0] as Tokens.Paragraph).tokens;
	}

	it("parses a bare wikilink", () => {
		const [token] = inlineTokens("[[context-window]]").filter((t) => t.type === "wikilink");
		expect(token).toMatchObject({ target: "context-window", label: "context-window", anchor: "" });
	});

	it("parses an aliased wikilink", () => {
		const [token] = inlineTokens("see [[agents/compaction|the note]] here").filter((t) => t.type === "wikilink");
		expect(token).toMatchObject({ target: "agents/compaction", label: "the note" });
	});

	it("parses a heading anchor", () => {
		const [token] = inlineTokens("[[compaction#what survives]]").filter((t) => t.type === "wikilink");
		expect(token).toMatchObject({ target: "compaction", anchor: "what survives" });
	});

	it("leaves an ordinary markdown link alone", () => {
		const tokens = inlineTokens("[text](https://example.com)");
		expect(tokens.some((t) => t.type === "wikilink")).toBe(false);
		expect(tokens.some((t) => t.type === "link")).toBe(true);
	});
});

describe("splitTags", () => {
	it("finds a tag at the start of a line and after whitespace", () => {
		expect(splitTags("#llm and #agents/subagents")).toEqual([
			{ kind: "tag", tag: "llm" },
			{ kind: "text", text: " and " },
			{ kind: "tag", tag: "agents/subagents" },
		]);
	});

	it("does not treat a mid-word hash as a tag", () => {
		expect(splitTags("C# and issue#12")).toEqual([{ kind: "text", text: "C# and issue#12" }]);
	});

	it("matches a non-latin tag", () => {
		expect(splitTags("#ความรู้")).toEqual([{ kind: "tag", tag: "ความรู้" }]);
	});

	it("returns the whole string when there is no tag", () => {
		expect(splitTags("plain text")).toEqual([{ kind: "text", text: "plain text" }]);
	});
});

describe("callouts", () => {
	function firstBlockquote(markdown: string) {
		return parseNote(markdown).tokens.find((t) => t.type === "blockquote") as Tokens.Blockquote;
	}

	it("reads the kind and title", () => {
		const callout = readCallout(firstBlockquote("> [!warning] Careful\n> body text\n"));
		expect(callout?.kind).toBe("warning");
		expect(callout?.title).toBe("Careful");
	});

	it("falls back to the kind as its own title", () => {
		expect(readCallout(firstBlockquote("> [!note]\n> body\n"))?.title).toBe("Note");
	});

	it("maps an alias onto its kind", () => {
		expect(readCallout(firstBlockquote("> [!bug] Broken\n> body\n"))?.kind).toBe("danger");
	});

	it("tolerates a foldable marker", () => {
		expect(readCallout(firstBlockquote("> [!tip]- Folded\n> body\n"))?.kind).toBe("tip");
	});

	it("is null for an ordinary blockquote", () => {
		expect(readCallout(firstBlockquote("> just a quote\n"))).toBeNull();
	});

	it("keeps the body after the marker line", () => {
		const callout = readCallout(firstBlockquote("> [!note] Title\n> the body\n"));
		expect(callout?.tokens.length).toBeGreaterThan(0);
	});
});

describe("task lists", () => {
	it("marks each item's checked state", () => {
		const list = parseNote("- [x] done\n- [ ] todo\n").tokens.find((t) => t.type === "list") as Tokens.List;
		expect(list.items.map((i) => [i.task, i.checked])).toEqual([
			[true, true],
			[true, false],
		]);
	});
});

describe("safeHref", () => {
	it("allows http, https and mailto", () => {
		expect(safeHref("https://example.com/a")).toBe("https://example.com/a");
		expect(safeHref("http://example.com/")).toBe("http://example.com/");
		expect(safeHref("mailto:a@b.com")).toBe("mailto:a@b.com");
	});

	it("refuses anything that could execute or leave the app", () => {
		for (const bad of [
			"javascript:alert(1)",
			"JavaScript:alert(1)",
			"  javascript:alert(1)  ",
			"data:text/html,<script>alert(1)</script>",
			"file:///etc/passwd",
			"//evil.example.com",
			"vbscript:msgbox(1)",
			"./relative.md",
		]) {
			expect(safeHref(bad)).toBe("");
		}
	});
});

describe("countWords", () => {
	it("counts space-delimited words", () => {
		expect(countWords("one two three")).toBe(3);
	});

	it("approximates Thai, which is written without spaces", () => {
		// 10 Thai characters ≈ 2 words, rather than the 1 a whitespace split gives.
		expect(countWords("สวัสดีครับผ")).toBeGreaterThan(1);
	});
});

describe("raw HTML", () => {
	it("is carried as an inert token, never parsed", () => {
		const note = parseNote("<script>alert(1)</script>\n");
		const html = note.tokens.find((t) => t.type === "html");
		expect(html).toBeDefined();
		expect((html as Tokens.HTML).raw).toContain("<script>");
	});
});
