import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import {
	findBuildRoot,
	findXcodeBuildServer,
	findXcodeContainer,
	INSTALL_XCODE_BUILD_SERVER,
	resolveSwiftWorkspace,
	SHADOW_LINK,
	sourcekitConfigHome,
	SWIFTPM_NO_SYMBOLS,
} from "./swift-workspace";

/**
 * The Swift setup story, against a real filesystem rather than a mocked one -
 * the whole subject here is symlinks, `info.plist` contents and directories that
 * are or are not present, and a mock of `fs` would be asserting my own beliefs
 * about those rather than the behaviour.
 *
 * Every `unconfigured` case matters more than the happy path. An unconfigured
 * sourcekit-lsp initializes in ~60 ms, publishes diagnostics, answers
 * documentSymbol - and returns 0 hits for every ⌘click and 0 results for every
 * symbol query. The point of this module is that we never get there.
 */
let tmp: string;
let dataDir: string;
let derivedDataDir: string;
let checkout: string;
let xbs: string;

function writeInfoPlist(dir: string, workspacePath: string): void {
	fs.mkdirSync(dir, { recursive: true });
	fs.writeFileSync(
		path.join(dir, "info.plist"),
		`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>LastAccessedDate</key>
	<date>2026-08-26T11:17:59Z</date>
	<key>WorkspacePath</key>
	<string>${workspacePath}</string>
</dict>
</plist>
`,
	);
}

beforeEach(() => {
	tmp = fs.mkdtempSync(path.join(os.tmpdir(), "ao-swift-"));
	dataDir = path.join(tmp, "data");
	derivedDataDir = path.join(tmp, "DerivedData");
	checkout = path.join(tmp, "nter-ios-app");
	fs.mkdirSync(path.join(checkout, "NterWorkspace.xcworkspace"), { recursive: true });
	fs.mkdirSync(derivedDataDir, { recursive: true });
	xbs = path.join(tmp, "xcode-build-server");
	fs.writeFileSync(xbs, "#!/usr/bin/env python3\n");
});

afterEach(() => {
	fs.rmSync(tmp, { recursive: true, force: true });
});

const env = () => ({ AO_LSP_XCODE_BUILD_SERVER: xbs });

describe("findXcodeContainer", () => {
	test("prefers the workspace over the project", () => {
		fs.mkdirSync(path.join(checkout, "NterApp.xcodeproj"), { recursive: true });
		// 🗝 Not a style preference. With CocoaPods the .xcodeproj alone does not
		// know about the Pods targets, so binding it produces exactly the
		// half-configured server this module exists to prevent - cross-module
		// ⌘click into a Pod would return nothing, with no error.
		expect(findXcodeContainer(checkout)).toBe(path.join(checkout, "NterWorkspace.xcworkspace"));
	});

	test("falls back to a project when there is no workspace", () => {
		const plain = path.join(tmp, "plain");
		fs.mkdirSync(path.join(plain, "Thing.xcodeproj"), { recursive: true });
		expect(findXcodeContainer(plain)).toBe(path.join(plain, "Thing.xcodeproj"));
	});

	test("a directory with neither, and a directory that is not there, are both null", () => {
		expect(findXcodeContainer(path.join(tmp, "data"))).toBeNull();
		expect(findXcodeContainer(path.join(tmp, "nope"))).toBeNull();
	});
});

describe("findBuildRoot", () => {
	const container = () => path.join(checkout, "NterWorkspace.xcworkspace");

	test("matches THIS checkout's DerivedData by WorkspacePath, not the newest one", () => {
		// 🗝 The failure this prevents, and it is not hypothetical: the editor spike
		// picked "the newest .xcactivitylog on this machine" and got a DIFFERENT
		// branch's checkout. On a machine with a dozen AO worktrees of one iOS app,
		// each with its own DerivedData, that is the normal case rather than an
		// edge case - and the symptom is ⌘click landing in someone else's tree.
		const other = path.join(tmp, "other-worktree");
		writeInfoPlist(path.join(derivedDataDir, "NterWorkspace-newest"), `${other}/NterWorkspace.xcworkspace`);
		fs.utimesSync(path.join(derivedDataDir, "NterWorkspace-newest", "info.plist"), new Date(), new Date());
		writeInfoPlist(path.join(derivedDataDir, "NterWorkspace-ours"), container());

		expect(findBuildRoot(container(), derivedDataDir)).toBe(path.join(derivedDataDir, "NterWorkspace-ours"));
	});

	test("a prefix of our path is not our path", () => {
		writeInfoPlist(path.join(derivedDataDir, "NterWorkspace-x"), `${checkout}-old/NterWorkspace.xcworkspace`);
		expect(findBuildRoot(container(), derivedDataDir)).toBeNull();
	});

	test("directories with no info.plist are skipped rather than throwing", () => {
		fs.mkdirSync(path.join(derivedDataDir, "ModuleCache.noindex"), { recursive: true });
		writeInfoPlist(path.join(derivedDataDir, "NterWorkspace-ours"), container());
		expect(findBuildRoot(container(), derivedDataDir)).toBe(path.join(derivedDataDir, "NterWorkspace-ours"));
	});
});

describe("findXcodeBuildServer", () => {
	test("an override that does not exist resolves to nothing rather than to itself", () => {
		// Returning the path anyway would move the failure from a sentence a person
		// can act on to a spawn error inside sourcekit-lsp's own build-server
		// handling, where nothing surfaces it.
		expect(findXcodeBuildServer({ AO_LSP_XCODE_BUILD_SERVER: path.join(tmp, "nope") })).toBeNull();
	});

	test("found on PATH", () => {
		expect(findXcodeBuildServer({ PATH: `${tmp}:/nowhere` })).toBe(xbs);
	});
});

describe("resolveSwiftWorkspace", () => {
	test("unconfigured when xcode-build-server is missing, and says how to get it", () => {
		const resolved = resolveSwiftWorkspace({
			workspaceRoot: checkout,
			dataDir,
			// 🗝 The override, not an empty PATH. `findXcodeBuildServer` falls back to
			// the three usual install prefixes whatever PATH says - Electron's PATH
			// routinely lacks homebrew - so on a machine that HAS the tool an empty
			// PATH cannot express its absence, and this test failed there and passed
			// in CI. The override resolving to nothing is the same absence, stated in
			// a way the test controls.
			env: { PATH: "/nowhere", AO_LSP_XCODE_BUILD_SERVER: path.join(tmp, "no-xcode-build-server") },
			derivedDataDir,
		});
		expect(resolved.kind).toBe("unconfigured");
		expect(resolved).toMatchObject({ reason: INSTALL_XCODE_BUILD_SERVER });
		expect(INSTALL_XCODE_BUILD_SERVER).toContain("brew install xcode-build-server");
	});

	test("unconfigured when Xcode has never built THIS worktree, naming the container", () => {
		const resolved = resolveSwiftWorkspace({ workspaceRoot: checkout, dataDir, env: env(), derivedDataDir });
		expect(resolved.kind).toBe("unconfigured");
		if (resolved.kind !== "unconfigured") throw new Error("unreachable");
		expect(resolved.reason).toContain("NterWorkspace.xcworkspace");
		expect(resolved.reason).toContain("Build it in Xcode once");
		// And nothing was created: refusing has to be free, or a project that can
		// never be served accumulates shadow roots every time a file is opened.
		expect(fs.existsSync(path.join(dataDir, "lsp"))).toBe(false);
	});

	test("unconfigured, with a distinct reason, when there is no Swift project at all", () => {
		const empty = path.join(tmp, "empty");
		fs.mkdirSync(empty);
		const resolved = resolveSwiftWorkspace({ workspaceRoot: empty, dataDir, env: env(), derivedDataDir });
		expect(resolved).toMatchObject({ kind: "unconfigured" });
		if (resolved.kind !== "unconfigured") throw new Error("unreachable");
		expect(resolved.reason).toContain("Package.swift");
	});

	describe("a SwiftPM package", () => {
		let pkg: string;
		beforeEach(() => {
			pkg = path.join(tmp, "pkg");
			fs.mkdirSync(pkg);
			fs.writeFileSync(path.join(pkg, "Package.swift"), "// swift-tools-version:5.9\n");
		});

		test("needs no shadow root and no build server", () => {
			expect(resolveSwiftWorkspace({ workspaceRoot: pkg, dataDir, env: {}, derivedDataDir })).toMatchObject({
				kind: "swiftpm",
				lspRoot: pkg,
				documentRoot: pkg,
			});
		});

		test("says out loud that symbol search is off, and why", () => {
			// 🗝 Not a limitation to leave implicit. sourcekit-lsp indexes a package by
			// BUILDING it into `.build/index-build` inside the package - measured, and
			// it ignores both `--scratch-path` and `swiftPM.scratchPath` while doing
			// it. AO may not write to a user's checkout, so the index is off and the
			// palette has to say so instead of rendering an empty list.
			const resolved = resolveSwiftWorkspace({ workspaceRoot: pkg, dataDir, env: {}, derivedDataDir });
			if (resolved.kind !== "swiftpm") throw new Error("unreachable");
			expect(resolved.warning).toBe(SWIFTPM_NO_SYMBOLS);
			expect(resolved.warning).toMatch(/go to definition works/i);
		});
	});

	test("the sourcekit-lsp config turns background indexing off, under the data dir", () => {
		const pkg = path.join(tmp, "pkg2");
		fs.mkdirSync(pkg);
		fs.writeFileSync(path.join(pkg, "Package.swift"), "// swift-tools-version:5.9\n");
		resolveSwiftWorkspace({ workspaceRoot: pkg, dataDir, env: {}, derivedDataDir });
		const config = JSON.parse(
			fs.readFileSync(path.join(sourcekitConfigHome(dataDir), "sourcekit-lsp", "config.json"), "utf8"),
		);
		expect(config).toEqual({ backgroundIndexing: false });
		// ⚠️ And deliberately NOT `index.indexDatabasePath`: measured on the real iOS
		// app, the build server's value wins and 39 MB landed in
		// ~/Library/Caches/xcode-build-server anyway. The HOME shim is what handles
		// that, and carrying a setting that does not work would read as if it did.
		expect(config).not.toHaveProperty("index");
	});

	describe("with a real Xcode build present", () => {
		let buildRoot: string;
		beforeEach(() => {
			buildRoot = path.join(derivedDataDir, "NterWorkspace-ours");
			writeInfoPlist(buildRoot, path.join(checkout, "NterWorkspace.xcworkspace"));
			fs.mkdirSync(path.join(buildRoot, "Index.noindex", "DataStore"), { recursive: true });
		});

		test("the shadow root is a symlink and a buildServer.json, and NOTHING else", () => {
			const resolved = resolveSwiftWorkspace({ workspaceRoot: checkout, dataDir, env: env(), derivedDataDir });
			if (resolved.kind !== "buildServer") throw new Error(`expected buildServer, got ${resolved.kind}`);
			// 🗝 Two entries. The editor spike prescribed a rewritten 10 MB `.compile`
			// and ~205 rewritten filelists as well; measured against the real iOS app,
			// all four ⌘click targets resolve without any of it, because sourcekit-lsp
			// resolves the symlink before asking the build server.
			expect(fs.readdirSync(resolved.lspRoot).sort()).toEqual(["buildServer.json", SHADOW_LINK]);
			expect(fs.readlinkSync(path.join(resolved.lspRoot, SHADOW_LINK))).toBe(checkout);
			expect(resolved.documentRoot).toBe(path.join(resolved.lspRoot, SHADOW_LINK));
		});

		test("NOTHING is written into the user's checkout", () => {
			const before = fs.readdirSync(checkout).sort();
			resolveSwiftWorkspace({ workspaceRoot: checkout, dataDir, env: env(), derivedDataDir });
			expect(fs.readdirSync(checkout).sort()).toEqual(before);
		});

		test("the BSP's HOME is redirected under the data dir, and it is the BSP's alone", () => {
			const resolved = resolveSwiftWorkspace({ workspaceRoot: checkout, dataDir, env: env(), derivedDataDir });
			if (resolved.kind !== "buildServer") throw new Error("unreachable");
			const config = JSON.parse(fs.readFileSync(path.join(resolved.lspRoot, "buildServer.json"), "utf8"));
			// 🗝 xcode-build-server hardcodes ~/Library/Caches/xcode-build-server with
			// no env override and hands that path to sourcekit-lsp as its index
			// database - measured at 211 MB per workspace, in an OS app-data location
			// this app may not touch. The spike's own runs left 422 MB there.
			expect(config.argv[0]).toBe("/usr/bin/env");
			expect(config.argv[1]).toMatch(/^HOME=/);
			expect(config.argv[1].slice("HOME=".length).startsWith(`${dataDir}/`)).toBe(true);
			expect(config.argv[2]).toBe(xbs);
		});

		test("kind is `xcode`, so the flags refresh when the user next builds", () => {
			const resolved = resolveSwiftWorkspace({ workspaceRoot: checkout, dataDir, env: env(), derivedDataDir });
			if (resolved.kind !== "buildServer") throw new Error("unreachable");
			const config = JSON.parse(fs.readFileSync(path.join(resolved.lspRoot, "buildServer.json"), "utf8"));
			// `manual` would freeze the compile args at whatever the build was when
			// the editor first opened, which is the spike's unmeasured staleness
			// question. `xcode` re-parses the newest .xcactivitylog on its own.
			expect(config.kind).toBe("xcode");
			expect(config.workspace).toBe(path.join(checkout, "NterWorkspace.xcworkspace"));
			expect(config.build_root).toBe(buildRoot);
		});

		test("a stale symlink is REPAIRED, not left pointing at a deleted worktree", () => {
			const resolved = resolveSwiftWorkspace({ workspaceRoot: checkout, dataDir, env: env(), derivedDataDir });
			if (resolved.kind !== "buildServer") throw new Error("unreachable");
			const link = path.join(resolved.lspRoot, SHADOW_LINK);
			fs.unlinkSync(link);
			fs.symlinkSync(path.join(tmp, "somewhere-else"), link);

			resolveSwiftWorkspace({ workspaceRoot: checkout, dataDir, env: env(), derivedDataDir });
			// A dangling link is the one failure here that reads as a language-server
			// problem rather than a filesystem one: every ⌘click returns nothing.
			expect(fs.readlinkSync(link)).toBe(checkout);
		});

		test("a build with no index warns that symbol search will find nothing", () => {
			fs.rmSync(path.join(buildRoot, "Index.noindex"), { recursive: true });
			const resolved = resolveSwiftWorkspace({ workspaceRoot: checkout, dataDir, env: env(), derivedDataDir });
			if (resolved.kind !== "buildServer") throw new Error("unreachable");
			// ⌘click needs compile arguments and symbol search needs an index; they
			// come from different halves of a build, so "half of this works" is a
			// real state and the UI has to be able to say WHICH half.
			expect(resolved.warning).toContain("symbol search");
		});

		test("a build WITH an index carries no warning", () => {
			const resolved = resolveSwiftWorkspace({ workspaceRoot: checkout, dataDir, env: env(), derivedDataDir });
			expect(resolved).toMatchObject({ kind: "buildServer", warning: undefined });
		});
	});
});
