import { describe, expect, it } from "vitest";
import { fromTagAddsSomething, sourceLabel, splitFromTags, splitWikilinks } from "./wiki-task-text";

describe("splitFromTags", () => {
	it("lifts the tag and leaves the sentence reading as a sentence", () => {
		expect(splitFromTags("Ask design whether the empty state says anything (from: My active items)")).toEqual({
			text: "Ask design whether the empty state says anything",
			tags: ["My active items"],
		});
	});

	it("keeps a tag that is not at the end where the sentence had it", () => {
		expect(splitFromTags("Chase the train (from: Release) before Friday").text).toBe("Chase the train before Friday");
	});

	it("returns the row untouched when there is no tag", () => {
		expect(splitFromTags("Plain row")).toEqual({ text: "Plain row", tags: [] });
	});
});

describe("fromTagAddsSomething", () => {
	it("is false when the tag only names the section the row already sits in", () => {
		expect(fromTagAddsSomething("My active items", "My active items", "")).toBe(false);
		// Case is not identity: the vault writes both spellings.
		expect(fromTagAddsSomething("release", "Release", "")).toBe(false);
	});

	it("is false when the tag is only a date — the day group already said that", () => {
		expect(fromTagAddsSomething("2026-05-07", "My active items", "")).toBe(false);
	});

	it("is true when the tag names where the task actually came from", () => {
		expect(fromTagAddsSomething("2026-05-07 standup", "My active items", "")).toBe(true);
		expect(fromTagAddsSomething("chat 2026-04-30, Mobility HQ", "My active items", "")).toBe(true);
	});
});

describe("splitWikilinks", () => {
	it("splits a link out of the middle of a row and drops the brackets", () => {
		expect(splitWikilinks("tracked under [[STAR-2195]] now")).toEqual([
			{ kind: "text", value: "tracked under " },
			{ kind: "wikilink", target: "STAR-2195", anchor: "", label: "STAR-2195" },
			{ kind: "text", value: " now" },
		]);
	});

	it("reads the alias and the anchor the Notes tab reads", () => {
		expect(splitWikilinks("[[note#Heading|shown]]")).toEqual([
			{ kind: "wikilink", target: "note", anchor: "Heading", label: "shown" },
		]);
	});

	it("leaves anything that is not a link as text — vault content is not markup", () => {
		expect(splitWikilinks("a <script>alert(1)</script> row")).toEqual([
			{ kind: "text", value: "a <script>alert(1)</script> row" },
		]);
	});
});

describe("sourceLabel", () => {
	it("drops an underscore-prefixed basename: it is a convention, not a title", () => {
		expect(sourceLabel("Areas/mobile-development/_tasks.md")).toBe("mobile-development");
	});

	it("keeps a basename that distinguishes one note from its siblings", () => {
		expect(sourceLabel("Areas/frontier/roadmap.md")).toBe("frontier/roadmap");
	});

	it("survives a note at the root of the vault", () => {
		expect(sourceLabel("inbox.md")).toBe("inbox");
	});
});
