import { describe, expect, it } from "vitest";
import { headingTitle, noteBody, stripLeadingTitle } from "./WikiNoteView";

// The page shows the note's title in its own 29px heading. If the body then
// still opens with the same `# Heading`, the reader gets the words twice —
// which is exactly what a frontmatter strip that left its trailing newline
// behind caused: every match below anchors at the start of the body.
describe("noteBody", () => {
	it("drops YAML frontmatter and the blank line after it", () => {
		expect(noteBody("---\ntags: [a]\n---\n\n# Title\n\nbody\n")).toBe("# Title\n\nbody\n");
	});

	it("leaves a note without frontmatter alone", () => {
		expect(noteBody("# Title\n\nbody\n")).toBe("# Title\n\nbody\n");
	});

	it("does not mistake a leading horizontal rule for frontmatter", () => {
		expect(noteBody("body\n\n---\n\nmore\n")).toBe("body\n\n---\n\nmore\n");
	});
});

describe("headingTitle", () => {
	it("reads the opening heading through frontmatter", () => {
		expect(headingTitle("---\ntags: [a]\n---\n\n# Context compaction\n\nbody\n")).toBe("Context compaction");
	});

	it("is empty when the note does not open with one", () => {
		expect(headingTitle("Some prose first.\n\n# Later\n")).toBe("");
		expect(headingTitle("## Not a title\n")).toBe("");
	});
});

describe("stripLeadingTitle", () => {
	it("removes the heading the page is already showing", () => {
		expect(stripLeadingTitle("---\ntags: [a]\n---\n\n# Context compaction\n\nbody\n", "Context compaction")).toBe(
			"body\n",
		);
	});

	it("keeps a heading that is not the title", () => {
		expect(stripLeadingTitle("# Something else\n\nbody\n", "From frontmatter")).toBe("# Something else\n\nbody\n");
	});
});
