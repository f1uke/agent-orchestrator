import path from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, test, vi } from "vitest";
import type { LanguageServerSpec } from "./language-servers";
import { type LspProcess, type LspProcessOptions, type LspState, startLspProcess } from "./lsp-process";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const FAKE = path.join(HERE, "fake-language-server.mjs");

function fakeSpec(env: Record<string, string> = {}): LanguageServerSpec {
	return { languageId: "go", command: process.execPath, args: [FAKE], extensions: [".go"], env: () => env };
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
			args: [],
			extensions: [".go"],
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

	test("rss() reports a positive number for a live process", async () => {
		const { proc } = start(fakeSpec());
		await proc.initialized;
		expect(await proc.rss()).toBeGreaterThan(0);
	});
});
