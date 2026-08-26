import path from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, test, vi } from "vitest";
import type { LanguageServerSpec } from "./language-servers";
import {
	type LspProcess,
	type LspProcessOptions,
	type LspState,
	parsePsTable,
	startLspProcess,
	treeRss,
} from "./lsp-process";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const FAKE = path.join(HERE, "fake-language-server.mjs");

function fakeSpec(env: Record<string, string> = {}): LanguageServerSpec {
	return {
		languageId: "go",
		command: process.execPath,
		args: () => [FAKE],
		extensions: [".go"],
		indexReadiness: "progress",
		env: () => env,
	};
}

/** The same fake, declared as a server whose index must be ASKED about. */
function syncSpec(env: Record<string, string> = {}): LanguageServerSpec {
	return { ...fakeSpec(env), languageId: "swift", indexReadiness: "synchronize" };
}

const live: LspProcess[] = [];

function start(spec: LanguageServerSpec, over: Partial<LspProcessOptions> = {}) {
	const states: { state: LspState; detail?: string }[] = [];
	const messages: Record<string, unknown>[] = [];
	const proc = startLspProcess({
		spec,
		root: HERE,
		dataDir: "/tmp/ao-test-data",
		env: process.env,
		onState: (state, detail) => states.push({ state, detail }),
		onMessage: (m) => messages.push(m),
		...over,
	});
	live.push(proc);
	return { proc, states, messages };
}

afterEach(async () => {
	await Promise.all(live.splice(0).map((p) => p.stop("test cleanup")));
});

describe("startLspProcess", () => {
	test("initialize carries a REAL rootUri and workspaceFolders", async () => {
		// slice 1's blocker (1): monaco.lsp hardcodes rootUri:null and never answers
		// workspace/workspaceFolders, so a server cannot learn its root. Main owning
		// the handshake is what makes that impossible here - so assert it.
		const { proc } = start(fakeSpec());
		await proc.initialized;
		expect(proc.state).toBe("ready");
		// The fake echoes back the rootUri it received, as serverInfo.version.
		expect(proc.detail).toContain(`file://${HERE}`);
	});

	test("reports indexing until the work-done progress token drains", async () => {
		const { proc, states } = start(fakeSpec({ FAKE_LSP_PROGRESS: "1" }));
		await proc.initialized;
		// Gate the UI on readiness, not on latency: workspace/symbol returns WRONG
		// answers before the index settles, so `indexing` must be observable.
		expect(states.map((s) => s.state)).toContain("indexing");
		await vi.waitFor(() => expect(proc.state).toBe("ready"), { timeout: 3000 });
	});

	test("does NOT report ready the instant initialize returns", async () => {
		// 🗝 Found by the test above. gopls answers initialize in ~40 ms and only
		// THEN begins its multi-second package load, so treating the initialize
		// response as readiness reports "ready" during precisely the window where
		// workspace/symbol answers WRONG rather than empty. `ready` is withheld for
		// the settle window, and a progress that begins inside it wins.
		const { proc, states } = start(fakeSpec({ FAKE_LSP_PROGRESS: "1" }), { readinessSettleMs: 5_000 });
		await vi.waitFor(() => expect(states.map((s) => s.state)).toContain("indexing"), { timeout: 2000 });
		expect(states.map((s) => s.state)).not.toContain("ready");
		// The progress ends at 150ms; readiness then still waits out a fresh window.
		await vi.waitFor(() => expect(proc.state).toBe("ready"), { timeout: 8000 });
	});

	test("a `synchronize` server holds `indexing` until its index answers - and is USABLE meanwhile", async () => {
		// 🗝 The Swift half of readiness, and the reason it is not the Go one.
		// sourcekit-lsp emits NO `$/progress` at all - measured, 45 s of listening
		// on a real iOS app, not one notification - so a progress-driven gate
		// settles at 400 ms and reports `ready` while the index is still loading.
		// It has to be ASKED instead.
		const { proc, states } = start(syncSpec({ FAKE_LSP_SYNCHRONIZE_MS: "600" }));
		// `initialized` resolves BEFORE the index does, on purpose: ⌘click needs
		// compile arguments rather than an index, so blocking here would cost every
		// Swift file six seconds of dead editor for a gate only symbols need.
		await proc.initialized;
		expect(proc.state).toBe("indexing");
		await vi.waitFor(() => expect(proc.state).toBe("ready"), { timeout: 5000 });
		expect(states.map((s) => s.state)).toEqual(["initializing", "indexing", "ready"]);
	});

	test("a server that cannot be asked becomes ready and SAYS its readiness is unknown", async () => {
		// An older sourcekit-lsp spells this `workspace/_pollIndex` and answers
		// -32601 here. It works; it just cannot be asked. Reporting plain `ready`
		// would turn a known limitation into an invisible one.
		const { proc } = start(syncSpec({ FAKE_LSP_SYNCHRONIZE_UNSUPPORTED: "1" }));
		await proc.initialized;
		await vi.waitFor(() => expect(proc.state).toBe("ready"), { timeout: 5000 });
		expect(proc.detail).toMatch(/index readiness is unknown/i);
	});

	test("an index that never finishes loading becomes ready with the reason, not a permanent spinner", async () => {
		const { proc } = start(syncSpec({ FAKE_LSP_SYNCHRONIZE_HANGS: "1" }), { indexTimeoutMs: 300 });
		await proc.initialized;
		expect(proc.state).toBe("indexing");
		await vi.waitFor(() => expect(proc.state).toBe("ready"), { timeout: 5000 });
		expect(proc.detail).toMatch(/still loading after/i);
	});

	test("the workspace detail `prepare` found survives a server that sends no serverInfo", async () => {
		// sourcekit-lsp sends none, and on the Swift path that detail is the only
		// thing the status pill has to show - which DerivedData the settings came
		// from, or which half of the build is missing.
		const { proc } = start(syncSpec({ FAKE_LSP_NO_SERVER_INFO: "1" }), {
			initialDetail: "Xcode build settings from NterWorkspace-abc",
		});
		await proc.initialized;
		expect(proc.detail).toBe("Xcode build settings from NterWorkspace-abc");
	});

	test("answers workspace/configuration itself so the server does not stall", async () => {
		// The fake exits 3 if its configuration request goes unanswered.
		const { proc } = start(fakeSpec({ FAKE_LSP_ASK_CONFIG: "1" }));
		await proc.initialized;
		await new Promise((r) => setTimeout(r, 700));
		expect(proc.state).toBe("ready");
	});

	test("a hung initialize becomes `failed`, not a spinner", async () => {
		const { proc } = start(fakeSpec({ FAKE_LSP_HANG_INITIALIZE: "1" }), { initializeTimeoutMs: 200 });
		await expect(proc.initialized).rejects.toThrow(/timed out/i);
		expect(proc.state).toBe("failed");
		expect(proc.detail).toMatch(/timed out/i);
	});

	test("a command that does not exist becomes `failed` with the command named", async () => {
		// The PATH trap: gopls lives at ~/go/bin/gopls, which Electron cannot see.
		// The visible outcome must NAME what was not found, never be silence.
		const spec: LanguageServerSpec = {
			languageId: "go",
			command: "definitely-not-a-real-binary-xyz",
			args: () => [],
			extensions: [".go"],
			indexReadiness: "progress",
			env: () => ({}),
		};
		const { proc } = start(spec);
		await expect(proc.initialized).rejects.toThrow();
		expect(proc.state).toBe("failed");
		expect(proc.detail).toContain("definitely-not-a-real-binary-xyz");
	});

	test("stop() uses the shutdown/exit handshake", async () => {
		const { proc } = start(fakeSpec());
		await proc.initialized;
		const pid = proc.pid as number;
		await proc.stop("test");
		expect(proc.state).toBe("stopped");
		expect(() => process.kill(pid, 0)).toThrow(); // gone
	});

	test("a server that ignores `exit` is SIGKILLed after the grace period", async () => {
		const { proc } = start(fakeSpec({ FAKE_LSP_IGNORE_SHUTDOWN: "1" }), { killGraceMs: 200 });
		await proc.initialized;
		const pid = proc.pid as number;
		await proc.stop("test");
		expect(() => process.kill(pid, 0)).toThrow();
	});

	test("forwards server-initiated traffic it does not own", async () => {
		const { proc, messages } = start(fakeSpec());
		await proc.initialized;
		proc.send({ jsonrpc: "2.0", id: 1, method: "workspace/symbol", params: { query: "x" } });
		await vi.waitFor(() => expect(messages.some((m) => m.id === 1)).toBe(true));
	});

	test("answers an UNSUPPORTED server request rather than leaving it hanging", async () => {
		// 🗝 Measured against real gopls: a server→client request left unanswered
		// stalls the server silently. Its workspace never loaded, it sat at 24 MB,
		// and nothing anywhere reported a problem. MethodNotFound is a real answer.
		// The fake exits 4 if its window/showMessageRequest goes unanswered, which
		// would surface here as `failed`.
		const { proc } = start(fakeSpec({ FAKE_LSP_ASK_UNSUPPORTED: "1" }));
		await proc.initialized;
		await new Promise((r) => setTimeout(r, 700));
		expect(proc.state).toBe("ready");
	});

	test("rss() reports a positive number for a live process", async () => {
		const { proc } = start(fakeSpec());
		await proc.initialized;
		expect(await proc.rss()).toBeGreaterThan(0);
	});
});

/**
 * 🗝 What a language server COSTS is not what `ps -p <pid>` says.
 *
 * Measured on a real iOS app: one Swift server is sourcekit-lsp at 207 MB, an
 * xcode-build-server child at 19 MB, and a `SourceKitService` XPC service at
 * 390 MB carrying `ppid=1`, its own process group and session 0 - so no tree
 * walk reaches it and no OS relationship attributes it. The editor spike
 * published 246 MB for this; the real figure is ~620 MB, and the difference is
 * load-bearing for the two-server cap.
 */
describe("process accounting", () => {
	const TABLE = [
		"  100     1 212528 /Applications/Xcode.app/.../sourcekit-lsp",
		"  101   100  36256 /Applications/Xcode.app/.../Python",
		"  102     1 393472 /Applications/Xcode.app/.../SourceKitService.xpc/Contents/MacOS/SourceKitService",
		"  103   101   1024 /bin/sh",
		"garbage that ps never prints",
		"",
	].join("\n");

	test("parses ps output and ignores anything that is not a row", () => {
		const rows = parsePsTable(TABLE);
		expect(rows.map((r) => r.pid)).toEqual([100, 101, 102, 103]);
		expect(rows[0]).toMatchObject({ ppid: 1, rssMb: 208 });
		// A command with spaces in its path must survive intact, or the sidecar
		// match silently stops matching.
		expect(parsePsTable("  7     1  1024 /Apps/My App/Contents/MacOS/Thing")[0].command).toBe(
			"/Apps/My App/Contents/MacOS/Thing",
		);
	});

	test("tree RSS includes grandchildren, and a missing pid is null not zero", () => {
		// Null and 0 mean different things: gone, versus running and free. Health
		// reporting 0 MB for a dead server reads as a very efficient one.
		expect(treeRss(parsePsTable(TABLE), 100)).toBe(208 + 35 + 1);
		expect(treeRss(parsePsTable(TABLE), 999)).toBeNull();
	});

	test("a self-parented row cannot hang the walk", () => {
		expect(treeRss(parsePsTable("  5     5 1024 /x"), 5)).toBe(1);
	});
});
