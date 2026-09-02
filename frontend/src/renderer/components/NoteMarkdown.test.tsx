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
