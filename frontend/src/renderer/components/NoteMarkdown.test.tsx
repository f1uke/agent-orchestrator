import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { NoteMarkdown } from "./NoteMarkdown";

vi.mock("../lib/note/highlight", () => ({
	highlightCode: () => Promise.resolve(null),
	grammarFor: () => "",
}));

function draw(source: string, navigation?: { onOpenWikilink?: (t: string) => void; onOpenTag?: (t: string) => void }) {
	const view = render(<NoteMarkdown source={source} theme="dark" navigation={navigation} />);
	return view.container;
}

/** Every checkbox drawn, in document order, as checked/unchecked. */
function checkboxes(container: HTMLElement): boolean[] {
	return [...container.querySelectorAll(".note-prose__checkbox")].map((node) =>
		node.classList.contains("note-prose__checkbox--on"),
	);
}

/**
 * A task list must draw its marker ONCE, as a checkbox.
 *
 * `marked` reports a task item twice: as `task`/`checked` on the item, and as a
 * synthetic `checkbox` token whose `raw` is the literal "[x] ". That token is
 * BLOCK-level in a tight list and INLINE in a loose one, which is how the first
 * fix here covered only half the space — a nested (tight) task list drew the
 * checkbox and then "[ ]" as its own paragraph, pushing the item's text onto
 * the next line. These cover the whole space rather than the one case seen.
 */
describe("task lists", () => {
	it("draws one checkbox and no literal marker in a tight list", () => {
		const container = draw("- [ ] one\n- [x] two\n");
		expect(checkboxes(container)).toEqual([false, true]);
		expect(container.textContent).toBe("onetwo");
	});

	it("draws one checkbox and no literal marker in a loose list", () => {
		const container = draw("- [ ] one\n\n- [x] two\n");
		expect(checkboxes(container)).toEqual([false, true]);
		expect(container.textContent).toBe("onetwo");
	});

	it("draws a nested tight task list under a heading — the reported case", () => {
		const container = draw("## Tasks\n\n- Parent\n  - [ ] child one\n  - [x] child two\n");
		expect(checkboxes(container)).toEqual([false, true]);
		expect(container.textContent).toBe("TasksParentchild onechild two");
	});

	it("draws a nested loose task list", () => {
		const container = draw("## Tasks\n\n- Parent\n\n  - [ ] child one\n\n  - [x] child two\n");
		expect(checkboxes(container)).toEqual([false, true]);
		expect(container.textContent).toBe("TasksParentchild onechild two");
	});

	it("draws a task three levels down", () => {
		const container = draw("- a\n  - b\n    - [ ] deep\n");
		expect(checkboxes(container)).toEqual([false]);
		expect(container.textContent).toBe("abdeep");
	});

	it("treats an upper-case [X] as checked", () => {
		const container = draw("- [X] done\n");
		expect(checkboxes(container)).toEqual([true]);
		expect(container.textContent).toBe("done");
	});

	it("keeps square brackets that belong to the task's own text", () => {
		const container = draw("- [ ] fix the [bracket] thing\n");
		expect(checkboxes(container)).toEqual([false]);
		expect(container.textContent).toBe("fix the [bracket] thing");
	});

	it("does NOT turn a plain item that opens with a bracket into a checkbox", () => {
		const container = draw("- [note] something\n");
		expect(checkboxes(container)).toEqual([]);
		expect(container.textContent).toBe("[note] something");
	});

	it("mixes tasks and plain bullets in one loose list without printing a marker", () => {
		const container = draw("- plain bullet\n\n- [x] a task\n\n- another plain\n");
		expect(checkboxes(container)).toEqual([true]);
		expect(container.textContent).toBe("plain bulleta taskanother plain");
	});

	it("keeps an ordered task list's marker off the page", () => {
		const container = draw("1. [ ] first\n2. [x] second\n");
		expect(checkboxes(container)).toEqual([false, true]);
		expect(container.textContent).toBe("firstsecond");
	});

	it("drops the marker inside a callout too", () => {
		const container = draw("> [!note] Doing\n> - [ ] inside a callout\n");
		expect(checkboxes(container)).toEqual([false]);
		expect(container.textContent).toContain("inside a callout");
		expect(container.textContent).not.toContain("[ ]");
	});
});

/**
 * `[[wikilinks]]` are drawn the way Obsidian draws them: the link text, and
 * nothing of the syntax. The pill is what says it is a link.
 */
describe("wikilinks", () => {
	it("shows a bare link without its brackets", () => {
		const container = draw("see [[STAR-2195-Navigate-In-App-Loop]] for more");
		expect(container.textContent).toBe("see STAR-2195-Navigate-In-App-Loop for more");
	});

	it("shows an aliased link's alias", () => {
		const container = draw("see [[agents/compaction|the compaction note]] here");
		expect(container.textContent).toBe("see the compaction note here");
	});

	it("keeps the brackets off a link that cannot be navigated either", () => {
		const container = draw("[[orphan]]");
		expect(container.textContent).toBe("orphan");
		expect(container.querySelector("button")).toBeNull();
	});

	it("opens the note the link names, not the text it shows", async () => {
		const onOpenWikilink = vi.fn();
		draw("[[agents/compaction|the compaction note]]", { onOpenWikilink });
		screen.getByRole("button", { name: "the compaction note" }).click();
		expect(onOpenWikilink).toHaveBeenCalledWith("agents/compaction");
	});
});

/**
 * Pointing the reader at ONE line of a note — what a click on the Tasks tab's
 * source line ends up doing here.
 *
 * The mark is keyed off the SAME span index the editor uses, so it inherits
 * that module's refusal to guess: a construct whose bytes could not be located
 * is simply not pointed at.
 */
describe("revealing one line", () => {
	function editingFor(source: string) {
		return {
			content: source,
			sourceOffset: 0,
			openAt: null,
			onOpen: () => undefined,
			onCommit: () => undefined,
			onCancel: () => undefined,
			onToggleTask: () => undefined,
			busy: false,
		};
	}

	/** The whole line `needle` sits on, as a byte range in `source`. */
	function lineOf(source: string, needle: string) {
		const start = source.indexOf(needle);
		return { start, end: start + needle.length };
	}

	function drawRevealing(source: string, needle: string) {
		const view = render(
			<NoteMarkdown source={source} theme="dark" editing={editingFor(source)} reveal={lineOf(source, needle)} />,
		);
		return [...view.container.querySelectorAll("[data-reveal]")];
	}

	/**
	 * 🗝 Caught live, not in a test: a list token BEGINS on the same byte as its
	 * own first item, so a container that answered "that is me" swallowed the
	 * mark and drew nothing with it — the first row under every heading was
	 * unreachable. Containers pass the mark through; only what carries text
	 * consumes it.
	 */
	it("marks the FIRST row of a list, which its list token starts on too", () => {
		const marked = drawRevealing("## Release\n\n- [ ] first row\n- [ ] second row\n", "- [ ] first row");
		expect(marked).toHaveLength(1);
		expect(marked[0].tagName).toBe("LI");
		expect(marked[0].textContent).toBe("first row");
	});

	it("marks the task row it was pointed at, and only that one", () => {
		const marked = drawRevealing("- [ ] first row\n- [ ] second row\n", "- [ ] second row");
		expect(marked).toHaveLength(1);
		expect(marked[0].tagName).toBe("LI");
		expect(marked[0].textContent).toBe("second row");
	});

	/**
	 * 🗝 The parent bullet's bytes SURROUND the nested row, so a "contains"
	 * test would light up the parent and point confidently at the wrong thing.
	 * The mark goes to the block that STARTS on the line.
	 */
	it("marks the nested row rather than the bullet that contains it", () => {
		const marked = drawRevealing("- Parent\n  - [ ] child one\n  - [ ] child two\n", "- [ ] child two");
		expect(marked).toHaveLength(1);
		expect(marked[0].textContent).toBe("child two");
	});

	it("marks a heading, and marks nothing at all for a line no block starts on", () => {
		expect(drawRevealing("## Release\n\n- [ ] a row\n", "## Release")[0]?.textContent).toBe("Release");
		const source = "- [ ] a row\n\nsome prose\n";
		const blank = { start: source.indexOf("\n") + 1, end: source.indexOf("\n") + 1 };
		const view = render(<NoteMarkdown source={source} theme="dark" editing={editingFor(source)} reveal={blank} />);
		expect(view.container.querySelectorAll("[data-reveal]")).toHaveLength(0);
	});

	it("marks nothing when it was told to point at nothing", () => {
		const source = "- [ ] a row\n";
		const view = render(<NoteMarkdown source={source} theme="dark" editing={editingFor(source)} reveal={null} />);
		expect(view.container.querySelectorAll("[data-reveal]")).toHaveLength(0);
	});
});

describe("code block action", () => {
	const SOURCE = "```bash\npnpm test chat-widget\n```\n\n```bash\ncd a\nnpm test\n```\n\n```ts\nconst a = 1;\n```\n";
	const offer: import("../lib/chat-run").CodeAction = ({ lang, text }) =>
		lang === "bash" && !text.includes("\n") ? { label: "Ask Claude to run this", onRun: () => undefined } : undefined;

	it("draws no extra button when no action is given, as in a note or the wiki", () => {
		draw(SOURCE);
		expect(screen.queryByRole("button", { name: "Ask Claude to run this" })).toBeNull();
		expect(screen.getAllByRole("button", { name: "Copy code" })).toHaveLength(3);
	});

	it("draws the button only on the blocks the action names, next to Copy", () => {
		render(<NoteMarkdown source={SOURCE} theme="dark" codeAction={offer} />);
		const run = screen.getByRole("button", { name: "Ask Claude to run this" });
		expect(run).toHaveAttribute("title", "Ask Claude to run this");
		expect(run.parentElement).toContainElement(screen.getAllByRole("button", { name: "Copy code" })[0]);
		expect(screen.getAllByRole("button", { name: "Copy code" })).toHaveLength(3);
	});

	it("runs the action of the block that was clicked", async () => {
		const onRun = vi.fn();
		render(
			<NoteMarkdown
				source={"- item\n\n  ```bash\n  ls\n  ```\n"}
				theme="dark"
				codeAction={({ text }) => ({ label: "Ask Claude to run this", onRun: () => onRun(text) })}
			/>,
		);
		await userEvent.click(screen.getByRole("button", { name: "Ask Claude to run this" }));
		expect(onRun).toHaveBeenCalledExactlyOnceWith("ls");
	});
});

describe("code block wrap toggle", () => {
	const SOURCE = '```go\nlog.Printf("a very long line that would scroll sideways")\n```\n\n```ts\nconst a = 1;\n```\n';

	it("starts unwrapped, and wraps only the block whose toggle was pressed", async () => {
		const container = draw(SOURCE);
		const [first, second] = screen.getAllByRole("button", { name: "Wrap lines" });
		const [firstBody, secondBody] = container.querySelectorAll(".note-prose__code-body");
		expect(first).toHaveAttribute("aria-pressed", "false");
		expect(firstBody).not.toHaveClass("note-prose__code-body--wrap");

		await userEvent.click(first);
		expect(first).toHaveAttribute("aria-pressed", "true");
		expect(firstBody).toHaveClass("note-prose__code-body--wrap");
		expect(second).toHaveAttribute("aria-pressed", "false");
		expect(secondBody).not.toHaveClass("note-prose__code-body--wrap");

		await userEvent.click(first);
		expect(first).toHaveAttribute("aria-pressed", "false");
		expect(firstBody).not.toHaveClass("note-prose__code-body--wrap");
	});
});

describe("untrusted markdown", () => {
	it("draws a javascript: link as its text, not as a link", () => {
		const container = draw("Click [here](javascript:alert(1)) or [there](https://example.com/a).");
		expect(screen.getByText("here").closest("a")).toBeNull();
		expect(container.querySelectorAll("a")).toHaveLength(1);
		expect(screen.getByRole("link", { name: "there" })).toHaveAttribute("href", "https://example.com/a");
	});

	it("opens a mailto link and refuses a file: link", () => {
		draw("[mail](mailto:owner@example.com) and [disk](file:///etc/passwd)");
		expect(screen.getByRole("link", { name: "mail" })).toHaveAttribute("href", "mailto:owner@example.com");
		expect(screen.getByText("disk").closest("a")).toBeNull();
	});

	it("shows raw HTML as literal text and creates no element from it", () => {
		const container = draw('<script>alert("x")</script>\n\nText with <img src=x onerror=alert(1)> inline.');
		expect(container.querySelector("script")).toBeNull();
		expect(container.querySelector("img")).toBeNull();
		expect(container).toHaveTextContent('<script>alert("x")</script>');
		expect(container).toHaveTextContent("<img src=x onerror=alert(1)>");
	});
});

describe("tables", () => {
	it("puts a table inside its own horizontal scroll container", () => {
		const container = draw("| a | b | c |\n| --- | --- | --- |\n| 1 | 2 | 3 |\n");
		const table = container.querySelector("table");
		expect(table?.parentElement).toHaveClass("note-prose__table-scroll");
		expect(table?.querySelectorAll("tbody td")).toHaveLength(3);
	});
});
