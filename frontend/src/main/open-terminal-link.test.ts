import { describe, expect, it } from "vitest";
import { isAllowedTerminalLink } from "./open-terminal-link";

describe("isAllowedTerminalLink", () => {
	it("allows http and https links (agent output URLs)", () => {
		expect(isAllowedTerminalLink("http://example.com")).toBe(true);
		expect(isAllowedTerminalLink("https://example.com/a/b?c=1")).toBe(true);
	});

	it("allows file:// links so Claude Code / Superpowers .md file links open", () => {
		expect(isAllowedTerminalLink("file:///Users/me/notes/plan.md")).toBe(true);
	});

	it("refuses non-allowlisted schemes that could launch arbitrary handlers", () => {
		expect(isAllowedTerminalLink("javascript:alert(1)")).toBe(false);
		expect(isAllowedTerminalLink("smb://host/share")).toBe(false);
		expect(isAllowedTerminalLink("vscode://file/etc/passwd")).toBe(false);
		expect(isAllowedTerminalLink("mailto:me@example.com")).toBe(false);
	});

	it("refuses empty, non-string, and unparseable input", () => {
		expect(isAllowedTerminalLink("")).toBe(false);
		expect(isAllowedTerminalLink("not a url")).toBe(false);
		expect(isAllowedTerminalLink(undefined)).toBe(false);
		expect(isAllowedTerminalLink(42)).toBe(false);
	});
});
