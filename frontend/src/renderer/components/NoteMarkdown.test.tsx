import { render, screen } from "@testing-library/react";
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
