import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { NoteMarkdown } from "./NoteMarkdown";

// shiki loads a grammar chunk per language and needs neither DOM nor network in
// these assertions: the code block is drawn plain first and coloured after, and
// the plain draw is what a test can see synchronously.
vi.mock("../lib/note/highlight", () => ({
	highlightCode: () => Promise.resolve(null),
	grammarFor: () => "",
}));

function renderNote(source: string, navigation?: Parameters<typeof NoteMarkdown>[0]["navigation"]) {
	return render(<NoteMarkdown source={source} theme="dark" navigation={navigation} />);
}

describe("NoteMarkdown — vault content is untrusted input", () => {
	// The whole reason this renderer builds React elements instead of an HTML
	// string. A note is markdown off disk, possibly written by an agent, drawn
	// inside the app's own origin.
	it("shows a script tag as text and never puts it in the DOM", () => {
		const { container } = renderNote("<script>window.pwned = 1</script>\n");
		expect(container.querySelector("script")).toBeNull();
		expect(screen.getByText(/window\.pwned = 1/)).toBeInTheDocument();
	});

	it("shows an img tag as text rather than fetching it", () => {
		const { container } = renderNote('<img src="x" onerror="window.pwned = 1">\n');
		expect(container.querySelector("img")).toBeNull();
		expect(container.textContent).toContain("onerror");
	});

	it("refuses to make a javascript: link a link", () => {
		const { container } = renderNote("[click me](javascript:window.pwned=1)\n");
		expect(container.querySelector("a")).toBeNull();
		expect(screen.getByText("click me")).toBeInTheDocument();
	});

	it("refuses a file: link too", () => {
		const { container } = renderNote("[secrets](file:///etc/passwd)\n");
		expect(container.querySelector("a")).toBeNull();
	});

	it("opens a real link externally rather than navigating the app", () => {
		const { container } = renderNote("[docs](https://example.com/a)\n");
		const link = container.querySelector("a");
		expect(link).toHaveAttribute("href", "https://example.com/a");
		expect(link).toHaveAttribute("target", "_blank");
		expect(link?.getAttribute("rel")).toContain("noopener");
	});

	// A note's image path is vault-relative (unreachable) and a remote one would
	// make opening a note phone home. The alt text is what the reader gets.
	it("does not load images", () => {
		const { container } = renderNote("![a diagram](https://example.com/x.png)\n");
		expect(container.querySelector("img")).toBeNull();
		expect(screen.getByText("a diagram")).toBeInTheDocument();
	});
});

describe("NoteMarkdown — what an Obsidian vault actually contains", () => {
	it("renders headings and prose", () => {
		renderNote("# Title\n\nSome body text.\n");
		expect(screen.getByRole("heading", { level: 1, name: "Title" })).toBeInTheDocument();
		expect(screen.getByText("Some body text.")).toBeInTheDocument();
	});

	it("draws a #tag as a pill that searches when clicked", async () => {
		const onOpenTag = vi.fn();
		renderNote("about #llm today\n", { onOpenTag });
		const tag = screen.getByRole("button", { name: "#llm" });
		await userEvent.click(tag);
		expect(onOpenTag).toHaveBeenCalledWith("llm");
	});

	it("draws a [[wikilink]] with its brackets and opens the note it names", async () => {
		const onOpenWikilink = vi.fn();
		renderNote("see [[context-window]] for more\n", { onOpenWikilink });
		const link = screen.getByRole("button", { name: "[[context-window]]" });
		await userEvent.click(link);
		expect(onOpenWikilink).toHaveBeenCalledWith("context-window");
	});

	it("shows only the alias of an aliased wikilink", () => {
		renderNote("see [[agents/compaction|the note]]\n", { onOpenWikilink: vi.fn() });
		expect(screen.getByRole("button", { name: "the note" })).toBeInTheDocument();
	});

	it("draws a task list with its checked state", () => {
		const { container } = renderNote("- [x] measured\n- [ ] not yet\n");
		expect(screen.getByText("measured")).toBeInTheDocument();
		expect(container.querySelectorAll(".note-prose__checkbox--on")).toHaveLength(1);
		expect(container.querySelectorAll(".note-prose__task-text--done")).toHaveLength(1);
	});

	// A blank line between bullets makes CommonMark ONE loose list, so a vault
	// that writes its plain bullets and its checkboxes as separate-looking
	// blocks arrives as a single mixed list. Handling tasks per LIST instead of
	// per item printed every "- [x]" as the literal text "[x]".
	it("draws checkboxes on the task items of a mixed list, and bullets on the rest", () => {
		const { container } = renderNote("- plain one\n- plain two\n\n- [x] done\n- [ ] todo\n");
		expect(container.textContent).not.toContain("[x]");
		expect(container.textContent).not.toContain("[ ]");
		expect(container.querySelectorAll(".note-prose__checkbox")).toHaveLength(2);
		expect(container.querySelectorAll(".note-prose__checkbox--on")).toHaveLength(1);
		// The two plain bullets keep their marker; the two tasks give theirs up.
		expect(container.querySelectorAll(".note-prose__li--task")).toHaveLength(2);
	});

	it("renders a numbered list as a numbered list", () => {
		const { container } = renderNote("1. first\n2. second\n");
		expect(container.querySelector("ol.note-prose__list")).not.toBeNull();
	});

	// The tiny uppercase tag is the KIND; the author's own title sits beside it
	// in the case they wrote it, because uppercasing a sentence shouts it.
	it("draws a callout with its kind tag and the author's title unshouted", () => {
		const { container } = renderNote("> [!warning] Measured, not documented\n> the body\n");
		expect(container.querySelector(".note-prose__callout--warning")).not.toBeNull();
		expect(container.querySelector(".note-prose__callout-kind")).toHaveTextContent("Warning");
		expect(container.querySelector(".note-prose__callout-title")).toHaveTextContent("Measured, not documented");
		expect(screen.getByText("the body")).toBeInTheDocument();
	});

	it("shows only the kind when the callout has no title of its own", () => {
		const { container } = renderNote("> [!note]\n> the body\n");
		expect(container.querySelector(".note-prose__callout-kind")).toHaveTextContent("Note");
		expect(container.querySelector(".note-prose__callout-title")).toBeNull();
	});

	it("draws an ordinary blockquote as a quote, not a callout", () => {
		const { container } = renderNote("> just a quote\n");
		expect(container.querySelector(".note-prose__callout")).toBeNull();
		expect(container.querySelector(".note-prose__quote")).not.toBeNull();
	});

	it("gives a fenced code block a language header and its code", () => {
		renderNote("```python\nprint(1)\n```\n");
		expect(screen.getByText("python")).toBeInTheDocument();
		expect(screen.getByText(/print\(1\)/)).toBeInTheDocument();
	});

	it("labels an unfenced code block as text rather than leaving the header blank", () => {
		renderNote("```\nplain\n```\n");
		expect(screen.getByText("text")).toBeInTheDocument();
	});

	it("renders a table", () => {
		renderNote("| a | b |\n| --- | --- |\n| 1 | 2 |\n");
		expect(screen.getByRole("table")).toBeInTheDocument();
		expect(screen.getByRole("columnheader", { name: "a" })).toBeInTheDocument();
	});

	// With no navigation wired (a preview, say) a tag and a wikilink must still
	// read correctly — as text, not as dead buttons.
	it("renders tags and wikilinks inertly when there is nowhere to navigate", () => {
		renderNote("#llm and [[note]]\n");
		expect(screen.queryByRole("button")).toBeNull();
		expect(screen.getByText("#llm")).toBeInTheDocument();
		expect(screen.getByText("[[note]]")).toBeInTheDocument();
	});
});
