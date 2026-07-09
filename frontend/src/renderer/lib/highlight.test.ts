import { describe, expect, it } from "vitest";
import { highlightLine, languageForPath } from "./highlight";

describe("languageForPath", () => {
	it("maps a nested Swift file to swift", () => {
		expect(languageForPath("a/b/File.swift")).toBe("swift");
	});

	it("maps a .tsx file to typescript", () => {
		expect(languageForPath("x.tsx")).toBe("typescript");
	});

	it("returns null for an extensionless file", () => {
		expect(languageForPath("Makefile")).toBeNull();
	});

	it("returns null for an unknown extension", () => {
		expect(languageForPath(".unknownext")).toBeNull();
	});
});

describe("highlightLine", () => {
	it("marks up a known language with hljs- classes", () => {
		const html = highlightLine("let x = 1", "swift");
		expect(html).toContain("hljs-");
		expect(html).toContain("x");
	});

	it("escapes a script tag instead of emitting it raw (security)", () => {
		const html = highlightLine("<script>", null);
		expect(html).toBe("&lt;script&gt;");
		expect(html).not.toContain("<script>");
	});

	it("escapes angle brackets and ampersands with no language", () => {
		const html = highlightLine("a < b && c", null);
		expect(html).toContain("&lt;");
		expect(html).toContain("&amp;");
		expect(html).not.toMatch(/[^&]<[^&]/);
	});
});
