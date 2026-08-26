import { describe, expect, test } from "vitest";
import { DEFAULT_GOMEMLIMIT, languageIdForPath, serverForLanguage, SWIFT_MEMORY_BOUND } from "./language-servers";

describe("serverForLanguage", () => {
	test("go maps to gopls in stdio mode", () => {
		const spec = serverForLanguage("go");
		expect(spec?.command).toBe("gopls");
		expect(spec?.args({ dataDir: "/d" })).toEqual(["-mode=stdio"]);
	});

	test("swift's sourcekit-lsp config home is under the data dir too", () => {
		// It carries `backgroundIndexing: false`, which is what keeps sourcekit-lsp
		// from writing `.build/index-build` into a user's Swift package.
		const home = serverForLanguage("swift")!.env({ dataDir: "/Users/x/.ao/data", env: {} }).XDG_CONFIG_HOME;
		expect(home.startsWith("/Users/x/.ao/data/")).toBe(true);
	});

	test("swift maps to sourcekit-lsp with both of its output paths redirected", () => {
		const spec = serverForLanguage("swift");
		expect(spec?.command).toBe("sourcekit-lsp");
		const args = spec!.args({ dataDir: "/Users/x/.ao/data" });
		// Both default INTO the user's checkout - `<workspace>/.build` and generated
		// interfaces beside the sources - so this is the AO hard rule AND the
		// "never write into their repo" rule, in one assertion.
		expect(args).toContain("--scratch-path");
		expect(args).toContain("--generated-files-path");
		for (const arg of args.filter((a) => a.startsWith("/"))) {
			expect(arg.startsWith("/Users/x/.ao/data/")).toBe(true);
		}
	});

	test("a language this slice does not ship returns null", () => {
		// TypeScript is slice 7. Returning a spec we cannot serve is the
		// silent-failure shape: a server spawns, answers nothing, looks fine.
		expect(serverForLanguage("typescript")).toBeNull();
		// Objective-C deliberately too: sourcekit-lsp serves it, but the registry
		// keys by language id, so claiming it would spawn a SECOND sourcekit-lsp
		// (~620 MB) for one iOS workspace.
		expect(serverForLanguage("objective-c")).toBeNull();
	});

	test("only Go declares a memory bound, and Swift's absence is deliberate", () => {
		// There is no sourcekit-lsp equivalent of GOMEMLIMIT and no flag for it, so
		// the only bound on Swift is the registry's cap plus the idle stop. Pinned
		// so that adding one later is a decision rather than a discovery.
		expect(SWIFT_MEMORY_BOUND).toBeNull();
		const swiftEnv = serverForLanguage("swift")!.env({ dataDir: "/d", env: {} });
		expect(Object.keys(swiftEnv)).toEqual(["XDG_CONFIG_HOME"]);
		expect(swiftEnv).not.toHaveProperty("GOMEMLIMIT");
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
	test("resolves .go and .swift, and nothing else in this slice", () => {
		expect(languageIdForPath("backend/internal/x.go")).toBe("go");
		expect(languageIdForPath("/abs/Main.swift")).toBe("swift");
		expect(languageIdForPath("NterApp/AppDelegate.SWIFT")).toBe("swift");
		// Objective-C is served by sourcekit-lsp and is still not ours: the registry
		// keys by language id, so claiming it would spawn a SECOND sourcekit-lsp
		// (~620 MB) for one iOS workspace.
		expect(languageIdForPath("Legacy/FBSDKServerConfiguration.m")).toBeNull();
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
		expect(spec?.args({ dataDir: "/d" })).toEqual(["fake.mjs"]);
	});
});
