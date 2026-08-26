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
		await expect(r.attach({ root: HERE, languageId: "swift" })).rejects.toThrow(/swift/);
	});
});

describe("idle lifecycle", () => {
	test("the last detach stops the server only after the grace period", async () => {
		const r = make({ idleGraceMs: 250 });
		const a = await r.attach({ root: HERE, languageId: "go" });
		r.detach(a.handleId);
		// The grace is load-bearing: closing one Go file and opening another must
		// not pay gopls's multi-second cold start again.
		expect((await r.health())[0].state).toBe("ready");
		await vi.waitFor(async () => expect((await r.health()).length).toBe(0), { timeout: 3000 });
	});

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
