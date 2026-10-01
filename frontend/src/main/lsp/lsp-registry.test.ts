import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, test, vi } from "vitest";
import { createLspRegistry, type LspRegistry, type LspRegistryOptions } from "./lsp-registry";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const FAKE = path.join(HERE, "fake-language-server.mjs");

const live: LspRegistry[] = [];

// The catalogue's `go` entry is redirected at the fake server through the
// test-only env override, so what is under test here is the registry's POLICY
// rather than gopls.
function make(over: Partial<LspRegistryOptions> = {}): LspRegistry {
	const registry = createLspRegistry({
		dataDir: "/tmp/ao-test-data",
		env: () => ({ ...process.env, AO_LSP_COMMAND_GO: process.execPath, AO_LSP_ARGS_GO: FAKE }),
		onState: () => {},
		onMessage: () => {},
		idleGraceMs: 100,
		readinessSettleMs: 20,
		...over,
	});
	live.push(registry);
	return registry;
}

afterEach(async () => {
	await Promise.all(live.splice(0).map((r) => r.disposeAll()));
});

describe("keying", () => {
	test("two attachments to the same (language, root) share ONE server", async () => {
		const r = make();
		const a = await r.attach({ root: HERE, languageId: "go" });
		const b = await r.attach({ root: HERE, languageId: "go" });
		expect(a.key).toBe(b.key);
		expect(a.handleId).not.toBe(b.handleId);
		const health = await r.health();
		expect(health).toHaveLength(1);
		expect(health[0].attachments).toBe(2);
	});

	test("the attachment carries the server's semantic-token legend to the renderer", async () => {
		// The renderer cannot ask the server itself - main owns the handshake - so
		// if this does not travel with the attachment, semantic highlighting has
		// nothing to decode against and simply never appears.
		const r = make();
		const a = await r.attach({ root: HERE, languageId: "go" });
		expect(a.semanticTokens).toEqual({
			tokenTypes: ["property", "identifier"],
			tokenModifiers: ["declaration", "defaultLibrary"],
		});
	});

	test("two roots get two servers - a server cannot be shared across worktrees", async () => {
		// The spike claimed one-server-per-workspace dedupes across AO sessions. It
		// does not: every session has its OWN worktree, a different directory with
		// different contents, and gopls holds a type graph per tree. This test pins
		// the real behaviour so the claim is not repeated.
		const r = make();
		await r.attach({ root: HERE, languageId: "go" });
		await r.attach({ root: path.dirname(HERE), languageId: "go" });
		expect(await r.health()).toHaveLength(2);
	});

	test("a language with no server in the catalogue rejects by name", async () => {
		const r = make();
		// A missing server is a REJECTION, unlike a workspace waiting to be set up:
		// nothing the reader does will make one appear.
		await expect(r.attach({ root: HERE, languageId: "cobol" })).rejects.toThrow(/no language server for "cobol"/);
	});
});

describe("idle lifecycle", () => {
	test("the last detach stops the server only after the grace period", async () => {
		// 🗝 The grace has to dwarf one `health()` call: it measures RSS with `ps`
		// BEFORE it reads the state, and on a machine running the whole suite in
		// parallel that took longer than the 250 ms this used to allow - so the
		// server was legitimately gone by the time "still ready?" was read.
		const r = make({ idleGraceMs: 2000 });
		const a = await r.attach({ root: HERE, languageId: "go" });
		r.detach(a.handleId);
		// The grace is load-bearing: closing one Go file and opening another must
		// not pay gopls's multi-second cold start again.
		expect((await r.health())[0].state).toBe("ready");
		await vi.waitFor(async () => expect((await r.health()).length).toBe(0), { timeout: 8000 });
	}, 15_000);

	test("re-attaching inside the grace period keeps the same server alive", async () => {
		const r = make({ idleGraceMs: 400 });
		const a = await r.attach({ root: HERE, languageId: "go" });
		const pid = (await r.health())[0].pid;
		r.detach(a.handleId);
		const b = await r.attach({ root: HERE, languageId: "go" });
		expect(b.key).toBe(a.key);
		await new Promise((res) => setTimeout(res, 700));
		expect((await r.health())[0].pid).toBe(pid);
	});
});

describe("the cap", () => {
	test("a third workspace evicts the least-recently-USED, not the unreferenced one", async () => {
		const r = make({ maxServers: 2, idleGraceMs: 60_000 });
		const a = await r.attach({ root: HERE, languageId: "go" });
		const b = await r.attach({ root: path.dirname(HERE), languageId: "go" });
		// `a` is still referenced but is now the least recently used. Evicting the
		// workspace nobody is looking at is right even when a pane still holds it -
		// and the pane self-heals, because it is told `stopped`.
		r.send(b.handleId, { jsonrpc: "2.0", id: 1, method: "workspace/symbol", params: { query: "x" } });
		const c = await r.attach({ root: path.dirname(path.dirname(HERE)), languageId: "go" });
		const keys = (await r.health()).map((h) => h.key);
		expect(keys).toHaveLength(2);
		expect(keys).not.toContain(a.key);
		expect(keys).toContain(c.key);
	});

	test("an evicted attachment is told `stopped` so the renderer can self-heal", async () => {
		const events: { handleId: string; state: string }[] = [];
		const r = make({ maxServers: 1, idleGraceMs: 60_000, onState: (e) => events.push(e) });
		const a = await r.attach({ root: HERE, languageId: "go" });
		await r.attach({ root: path.dirname(HERE), languageId: "go" });
		// Silence here is the bug: a pane whose server vanished with no event sits
		// there with no intelligence and no error - the spike's carried bug.
		await vi.waitFor(() => expect(events.some((e) => e.handleId === a.handleId && e.state === "stopped")).toBe(true));
	});
});

describe("health", () => {
	test("counts empty-while-ready separately from errors", async () => {
		const r = make();
		const a = await r.attach({ root: HERE, languageId: "go" });
		r.noteResult(a.handleId, "empty");
		r.noteResult(a.handleId, "empty");
		r.noteResult(a.handleId, "error");
		r.noteResult(a.handleId, "ok");
		const h = (await r.health())[0];
		// A server that is up, answering, and returning empty must be
		// DISTINGUISHABLE from one that is working.
		expect(h.emptyWhileReady).toBe(2);
		expect(h.errors).toBe(1);
		expect(h.requests).toBe(4);
	});

	test("reports RSS for a live server", async () => {
		const r = make();
		await r.attach({ root: HERE, languageId: "go" });
		expect((await r.health())[0].rssMb).toBeGreaterThan(0);
	});
});

describe("disposeAll", () => {
	test("stops every server", async () => {
		const r = make({ idleGraceMs: 60_000 });
		await r.attach({ root: HERE, languageId: "go" });
		await r.attach({ root: path.dirname(HERE), languageId: "go" });
		await r.disposeAll();
		expect(await r.health()).toHaveLength(0);
	});
});

describe("a workspace that cannot be served", () => {
	test("attach DECLINES with the reason, and spawns nothing", async () => {
		// 🗝 The whole reason `prepare` exists. Pointed at a real .xcodeproj with no
		// build settings, sourcekit-lsp initializes in ~60 ms, publishes
		// diagnostics and answers documentSymbol - while returning 0 hits for every
		// ⌘click and 0 results for every symbol query, with no error anywhere. A
		// user who gets that concludes the feature does not work. A user who gets
		// "build it in Xcode once" fixes it in a minute.
		const r = make({
			env: () => ({
				...process.env,
				AO_LSP_COMMAND_SWIFT: process.execPath,
				AO_LSP_ARGS_SWIFT: FAKE,
				// No xcode-build-server, no Xcode container: `prepare` says no.
				PATH: "/nowhere",
				HOME: path.join(HERE, "no-such-home"),
			}),
		});
		const attachment = await r.attach({ root: HERE, languageId: "swift" });
		expect(attachment.state).toBe("unconfigured");
		expect(attachment.detail).toMatch(/Package\.swift|xcode/i);
		expect(await r.health()).toHaveLength(0);
	});
});

/**
 * 🗝 The 2026-10-02 report, end to end through the registry: a moved worktree
 * whose new path Xcode had never built. The pane must say it is waiting, spawn
 * NOTHING while it waits, and come alive by itself once the build lands -
 * without anyone closing and reopening the file.
 */
describe("a Swift worktree waiting for its first Xcode build", () => {
	let tmp: string;
	let worktree: string;
	let derivedData: string;
	let swiftEnv: () => NodeJS.ProcessEnv;

	const writeBuild = () => {
		const dir = path.join(derivedData, "NterWorkspace-fresh");
		fs.mkdirSync(dir, { recursive: true });
		fs.writeFileSync(
			path.join(dir, "info.plist"),
			`<plist><dict><key>WorkspacePath</key><string>${path.join(worktree, "NterWorkspace.xcworkspace")}</string></dict></plist>`,
		);
	};

	afterEach(() => {
		fs.rmSync(tmp, { recursive: true, force: true });
	});

	const setUp = () => {
		tmp = fs.mkdtempSync(path.join(os.tmpdir(), "ao-lsp-wait-"));
		worktree = path.join(tmp, "nter-ios-app");
		fs.mkdirSync(path.join(worktree, "NterWorkspace.xcworkspace"), { recursive: true });
		// `defaultDerivedDataDir` is `$HOME/Library/Developer/Xcode/DerivedData`.
		derivedData = path.join(tmp, "home", "Library", "Developer", "Xcode", "DerivedData");
		fs.mkdirSync(derivedData, { recursive: true });
		const xbs = path.join(tmp, "xcode-build-server");
		fs.writeFileSync(xbs, "#!/bin/sh\n");
		swiftEnv = () => ({
			...process.env,
			HOME: path.join(tmp, "home"),
			AO_LSP_XCODE_BUILD_SERVER: xbs,
			AO_LSP_COMMAND_SWIFT: process.execPath,
			AO_LSP_ARGS_SWIFT: FAKE,
		});
	};

	test("waits without a process, then reports `stopped` once the build lands, and the re-attach serves", async () => {
		setUp();
		const events: { handleId: string; state: string; detail?: string }[] = [];
		const r = make({
			dataDir: path.join(tmp, "data"),
			env: () => swiftEnv(),
			setupPollMs: 20,
			onState: (event) => events.push(event),
		});

		const waiting = await r.attach({ root: worktree, languageId: "swift" });
		expect(waiting).toMatchObject({ state: "unconfigured", need: "build" });
		expect(waiting.detail).toMatch(/never built NterWorkspace\.xcworkspace/);
		// Nothing spawned, so nothing counts against the server cap.
		expect(await r.health()).toHaveLength(0);

		writeBuild();
		await vi.waitFor(() => expect(events.find((e) => e.handleId === waiting.handleId)?.state).toBe("stopped"), {
			timeout: 2_000,
		});

		// What the pane does on `stopped`: attach again. This time there is a server.
		r.detach(waiting.handleId);
		const served = await r.attach({ root: worktree, languageId: "swift" });
		expect(served.state).not.toBe("unconfigured");
		expect(served.documentRoot).not.toBe(worktree);
		expect(await r.health()).toHaveLength(1);
	});

	test("two panes on one waiting workspace share one wait, and the last detach ends it", async () => {
		setUp();
		const events: { handleId: string; state: string }[] = [];
		const r = make({
			dataDir: path.join(tmp, "data"),
			env: () => swiftEnv(),
			setupPollMs: 20,
			onState: (e) => events.push(e),
		});
		const a = await r.attach({ root: worktree, languageId: "swift" });
		const b = await r.attach({ root: worktree, languageId: "swift" });
		expect(a.handleId).not.toBe(b.handleId);
		r.detach(a.handleId);
		r.detach(b.handleId);
		writeBuild();
		await new Promise((resolve) => setTimeout(resolve, 100));
		// Nobody is waiting any more, so nobody is told.
		expect(events).toHaveLength(0);
	});

	test("a waiting pane hears it when the REASON changes, without being told to re-attach", async () => {
		setUp();
		const events: { handleId: string; state: string; need?: string }[] = [];
		const missing = path.join(tmp, "no-xcode-build-server");
		let xbsOverride = missing;
		const r = make({
			dataDir: path.join(tmp, "data"),
			env: () => ({ ...swiftEnv(), AO_LSP_XCODE_BUILD_SERVER: xbsOverride }),
			setupPollMs: 20,
			onState: (e) => events.push(e),
		});
		const waiting = await r.attach({ root: worktree, languageId: "swift" });
		expect(waiting.need).toBe("tool");

		xbsOverride = path.join(tmp, "xcode-build-server");
		await vi.waitFor(() => expect(events.at(-1)).toMatchObject({ state: "unconfigured", need: "build" }), {
			timeout: 2_000,
		});
		expect(events.some((e) => e.state === "stopped")).toBe(false);
	});
});

describe("the document root", () => {
	test("is the workspace root for a language that does not remap it", async () => {
		// Carried on every attachment rather than only the Swift ones, so the
		// renderer has exactly one rule to follow and no special case to forget.
		const r = make();
		const attachment = await r.attach({ root: HERE, languageId: "go" });
		expect(attachment.documentRoot).toBe(HERE);
	});
});
