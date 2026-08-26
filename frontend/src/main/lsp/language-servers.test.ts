import { describe, expect, test } from "vitest";
import { DEFAULT_GOMEMLIMIT, languageIdForPath, serverForLanguage } from "./language-servers";

describe("serverForLanguage", () => {
	test("go maps to gopls in stdio mode", () => {
		const spec = serverForLanguage("go");
		expect(spec?.command).toBe("gopls");
		expect(spec?.args).toEqual(["-mode=stdio"]);
	});

	test("a language this slice does not ship returns null", () => {
		// Swift is slice 5, TypeScript is slice 7. Returning a spec we cannot serve
		// is the silent-failure shape: a server spawns, answers nothing, looks fine.
		expect(serverForLanguage("swift")).toBeNull();
		expect(serverForLanguage("typescript")).toBeNull();
	});
});

describe("gopls env", () => {
	const spec = serverForLanguage("go");

	test("every cache it writes is pinned under the AO data dir", () => {
		const env = spec!.env({ dataDir: "/Users/x/.ao/data", env: {} });
		// The AO hard rule. gopls defaults to ~/Library/Caches/gopls, which we may
		// not use, so this assertion is the rule and not a preference.
		expect(env.GOPLSCACHE).toBe("/Users/x/.ao/data/lsp/gopls");
		for (const value of Object.values(env)) {
			expect(value).not.toContain("Library/Caches");
			expect(value).not.toContain("Library/Application Support");
		}
	});

	test("GOMEMLIMIT defaults to 1GiB and is overridable", () => {
		expect(spec!.env({ dataDir: "/d", env: {} }).GOMEMLIMIT).toBe(DEFAULT_GOMEMLIMIT);
		expect(spec!.env({ dataDir: "/d", env: { AO_LSP_GOMEMLIMIT: "2GiB" } }).GOMEMLIMIT).toBe("2GiB");
	});
});

describe("languageIdForPath", () => {
	test("resolves .go and nothing else in this slice", () => {
		expect(languageIdForPath("backend/internal/x.go")).toBe("go");
		expect(languageIdForPath("/abs/Main.swift")).toBeNull();
		expect(languageIdForPath("README.md")).toBeNull();
		expect(languageIdForPath("nodot")).toBeNull();
	});
});

describe("test-only command override", () => {
	test("AO_LSP_COMMAND_GO redirects the spawn so registry policy can be tested", () => {
		// Never set in production. It exists so the registry's lifecycle can be
		// exercised against a fake server on a machine without gopls.
		const spec = serverForLanguage("go", { AO_LSP_COMMAND_GO: "/usr/bin/node", AO_LSP_ARGS_GO: "fake.mjs" });
		expect(spec?.command).toBe("/usr/bin/node");
		expect(spec?.args).toEqual(["fake.mjs"]);
	});
});
