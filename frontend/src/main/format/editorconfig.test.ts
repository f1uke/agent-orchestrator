import { describe, expect, it } from "vitest";
import { globMatches, parseEditorConfig, resolveEditorConfig } from "./editorconfig";

describe("editorconfig globs", () => {
	it("matches a slash-less glob by file name at any depth", () => {
		expect(globMatches("*.go", "main.go")).toBe(true);
		expect(globMatches("*.go", "cmd/ao/main.go")).toBe(true);
		expect(globMatches("*.go", "main.gox")).toBe(false);
	});

	it("anchors a glob with a slash to the config's own directory", () => {
		expect(globMatches("lib/**.js", "lib/a/b.js")).toBe(true);
		expect(globMatches("lib/*.js", "lib/a/b.js")).toBe(false);
		expect(globMatches("/Makefile", "Makefile")).toBe(true);
		expect(globMatches("/Makefile", "sub/Makefile")).toBe(false);
	});

	it("reads sets, ranges, classes and escapes", () => {
		expect(globMatches("*.{ts,tsx}", "a.tsx")).toBe(true);
		expect(globMatches("*.{ts,tsx}", "a.js")).toBe(false);
		expect(globMatches("file{1..3}.txt", "file2.txt")).toBe(true);
		expect(globMatches("file{1..3}.txt", "file4.txt")).toBe(false);
		expect(globMatches("[!a]*.md", "b.md")).toBe(true);
		expect(globMatches("[!a]*.md", "a.md")).toBe(false);
		expect(globMatches("{single}.txt", "{single}.txt")).toBe(true);
		expect(globMatches("\\*.txt", "*.txt")).toBe(true);
		expect(globMatches("\\*.txt", "a.txt")).toBe(false);
	});
});

describe("resolveEditorConfig", () => {
	const files: Record<string, string> = {
		"/repo/.editorconfig": "root = true\n\n[*]\nindent_style = space\nindent_size = 4\n\n[*.go]\nindent_style = tab\n",
		"/repo/web/.editorconfig": "[*.ts]\nindent_size = 2\n",
		"/.editorconfig": "[*]\nindent_size = 8\n",
	};
	const readText = async (file: string) => files[file] ?? null;

	it("merges from the root config down, the nearer file and the later section winning", async () => {
		expect(await resolveEditorConfig("/repo/web/src/a.ts", readText)).toMatchObject({
			indent_style: "space",
			indent_size: "2",
		});
		expect(await resolveEditorConfig("/repo/cmd/main.go", readText)).toEqual({ indent_style: "tab", indent_size: "4" });
	});

	it("stops at root = true", async () => {
		expect((await resolveEditorConfig("/repo/x.py", readText)).indent_size).toBe("4");
	});

	it("reads the preamble's root flag, case-insensitively", () => {
		expect(parseEditorConfig("ROOT = TRUE\n[*]\nx = y\n").root).toBe(true);
	});
});
