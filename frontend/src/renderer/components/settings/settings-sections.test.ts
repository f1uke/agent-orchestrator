import { describe, expect, it } from "vitest";
import { matchesSearch, sectionsForScope } from "./settings-sections";

describe("project settings search", () => {
	it("finds the section holding the Testiny project when searching for testiny", () => {
		const hits = sectionsForScope("project").filter((section) => matchesSearch(section, "Testiny"));
		expect(hits.map((section) => section.key)).toEqual(["told"]);
	});
});

describe("global settings search", () => {
	it("finds the section holding the Claude profiles when searching for omniroute", () => {
		const hits = sectionsForScope("global").filter((section) => matchesSearch(section, "OmniRoute"));
		expect(hits.map((section) => section.key)).toEqual(["every-agent"]);
	});
});
