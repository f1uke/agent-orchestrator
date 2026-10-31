import { describe, expect, it } from "vitest";
import { opensInSystemBrowser } from "./external-link";

describe("opensInSystemBrowser", () => {
	it("opens http, https and mailto links", () => {
		expect(opensInSystemBrowser("https://github.com/example/billing/pull/42")).toBe(true);
		expect(opensInSystemBrowser("http://localhost:5561/")).toBe(true);
		expect(opensInSystemBrowser("mailto:owner@example.com")).toBe(true);
	});

	it("refuses every other scheme and anything that is not a URL", () => {
		for (const url of [
			"javascript:alert(1)",
			"file:///etc/passwd",
			"data:text/html,x",
			"ftp://host/a",
			"",
			"not a url",
		]) {
			expect(opensInSystemBrowser(url)).toBe(false);
		}
	});
});
