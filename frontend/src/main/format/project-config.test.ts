import { describe, expect, it } from "vitest";
import { ancestors, findLocalBin, findPrettierConfig, type Fs, resolveIndentStyle } from "./project-config";

function fakeFs(files: Record<string, string>): Fs {
	return {
		readText: async (file) => files[file] ?? null,
		exists: async (file) => file in files,
	};
}

describe("ancestors", () => {
	it("stops at the workspace root when it is an ancestor, and at / when it is not", () => {
		expect(ancestors("/repo/a/b", "/repo")).toEqual(["/repo/a/b", "/repo/a", "/repo"]);
		expect(ancestors("/elsewhere/x", "/repo")).toEqual(["/elsewhere/x", "/elsewhere", "/"]);
	});
});

describe("resolveIndentStyle", () => {
	it("indents Go with tabs whatever .editorconfig says, taking only its tab width", async () => {
		const fs = fakeFs({ "/repo/.editorconfig": "root=true\n[*]\nindent_style=space\nindent_size=2\ntab_width=4\n" });
		expect(
			await resolveIndentStyle({ filePath: "/repo/main.go", languageId: "go", workspaceRoot: "/repo" }, fs),
		).toMatchObject({
			insertSpaces: false,
			tabSize: 4,
		});
	});

	it("lets .swift-format decide Swift over .editorconfig", async () => {
		const fs = fakeFs({
			"/repo/.editorconfig": "root=true\n[*]\nindent_style=tab\n",
			"/repo/.swift-format": JSON.stringify({ version: 1, indentation: { spaces: 3 } }),
		});
		expect(await resolveIndentStyle({ filePath: "/repo/App/A.swift", languageId: "swift" }, fs)).toMatchObject({
			insertSpaces: true,
			indentSize: 3,
			source: "/repo/.swift-format",
		});
	});

	it("reads Prettier's useTabs/tabWidth, falling back to .editorconfig for what it leaves unset", async () => {
		const fs = fakeFs({
			"/repo/.editorconfig": "root=true\n[*]\nindent_style=tab\nindent_size=4\n",
			"/repo/.prettierrc": '{ "useTabs": true, "tabWidth": 2 }',
		});
		expect(
			await resolveIndentStyle({ filePath: "/repo/src/a.ts", languageId: "typescript", workspaceRoot: "/repo" }, fs),
		).toMatchObject({
			insertSpaces: false,
			indentSize: 2,
		});
		const yaml = fakeFs({
			"/repo/.prettierrc.yaml": "tabWidth: 4\n",
			"/repo/.editorconfig": "[*]\nindent_style = tab\n",
		});
		expect(
			await resolveIndentStyle({ filePath: "/repo/a.ts", languageId: "typescript", workspaceRoot: "/repo" }, yaml),
		).toMatchObject({
			insertSpaces: false,
			indentSize: 4,
		});
	});

	it("follows .editorconfig for everything else, and says nothing when nothing is configured", async () => {
		const fs = fakeFs({ "/repo/.editorconfig": "root=true\n[*.py]\nindent_size=4\n" });
		expect(await resolveIndentStyle({ filePath: "/repo/a.py", languageId: "python" }, fs)).toEqual({
			insertSpaces: undefined,
			indentSize: 4,
			tabSize: undefined,
			source: ".editorconfig",
		});
		expect(await resolveIndentStyle({ filePath: "/repo/a.rb", languageId: "ruby" }, fs)).toBeNull();
	});
});

describe("Prettier discovery", () => {
	it("counts a package.json prettier key, and never looks above the workspace root", async () => {
		const fs = fakeFs({
			"/repo/package.json": JSON.stringify({ name: "x", prettier: { useTabs: true } }),
			"/.prettierrc": "{}",
		});
		expect(await findPrettierConfig("/repo/src/a.ts", "/repo", fs)).toEqual({
			path: "/repo/package.json",
			useTabs: true,
		});
		expect(await findPrettierConfig("/other/a.ts", "/other", fs)).toBeNull();
	});

	it("finds the nearest local prettier binary", async () => {
		const fs = fakeFs({ "/repo/node_modules/.bin/prettier": "", "/repo/web/node_modules/.bin/prettier": "" });
		expect(await findLocalBin("prettier", "/repo/web/src/a.ts", "/repo", fs)).toBe(
			"/repo/web/node_modules/.bin/prettier",
		);
	});
});
