import { describe, expect, test } from "vitest";
import {
	languageDisplayName,
	languageIdForLsp,
	languageServerName,
	lspLanguageForPath,
	symbolLanguageForIndex,
} from "./language-ids";

describe("languageIdForLsp", () => {
	test("serves Go and Swift, and says no to everything else", () => {
		expect(languageIdForLsp("go")).toBe("go");
		expect(languageIdForLsp("swift")).toBe("swift");
		expect(languageIdForLsp("markdown")).toBeNull();
		// 🗝 sourcekit-lsp serves Objective-C, and claiming it here would spawn a
		// SECOND sourcekit-lsp (~620 MB) for one iOS workspace, because the registry
		// keys servers by language id. Pinned so re-adding it is a decision.
		expect(languageIdForLsp("objective-c")).toBeNull();
	});
});

describe("lspLanguageForPath", () => {
	test("reads the extension without dragging Monaco into the bundle", () => {
		expect(lspLanguageForPath("NterApp/AppDelegate.swift")).toBe("swift");
		expect(lspLanguageForPath("/abs/x.go")).toBe("go");
		expect(lspLanguageForPath("A.SWIFT")).toBe("swift");
		expect(lspLanguageForPath("Makefile")).toBeNull();
		expect(lspLanguageForPath(".gitignore")).toBeNull();
	});
});

describe("naming", () => {
	test("every served language has a display name and a server name", () => {
		// The status pill and the palette's failure line both read from these, so a
		// language with no name renders its raw id at a user.
		for (const id of ["go", "swift"]) {
			expect(languageDisplayName(id)).not.toBe(id);
			expect(languageServerName(id)).not.toBe(id);
		}
	});
});

describe("symbolLanguageForIndex", () => {
	test("picks the language the workspace is mostly made of", () => {
		// 🗝 ONE language, because the registry caps the app at two servers:
		// attaching to every language present would evict the pane the reader is
		// looking at just to answer a search, and on an iOS project it would pay
		// ~620 MB to search a handful of stray Go files.
		expect(symbolLanguageForIndex(["a.swift", "b.swift", "c.swift", "tools/gen.go"])).toBe("swift");
		expect(symbolLanguageForIndex(["a.go", "b.go", "Scripts/Build.swift"])).toBe("go");
	});

	test("no served language means no server is started at all", () => {
		// Without this, ⌘⇧O in a TypeScript-only repo spawns gopls in a directory
		// with no go.mod, every single time the palette opens.
		expect(symbolLanguageForIndex(["a.ts", "b.tsx", "README.md"])).toBeNull();
		expect(symbolLanguageForIndex([])).toBeNull();
		expect(symbolLanguageForIndex(undefined)).toBeNull();
	});

	test("a tie resolves the same way every time", () => {
		// A palette that alternates between two servers on identical input is one
		// you cannot learn - and it would thrash the two-server cap.
		const paths = ["a.go", "b.swift"];
		expect(symbolLanguageForIndex(paths)).toBe(symbolLanguageForIndex([...paths].reverse()));
	});
});
