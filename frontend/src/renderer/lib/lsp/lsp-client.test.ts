import { describe, expect, test, vi } from "vitest";
import { createLspClient as createRawLspClient, type LspTransport } from "./lsp-client";

function harness() {
	const sent: { handleId: string; message: Record<string, unknown> }[] = [];
	const outcomes: string[] = [];
	let emit: (e: { handleId: string; message: Record<string, unknown> }) => void = () => {};
	const transport: LspTransport = {
		send: (handleId, message) => sent.push({ handleId, message }),
		noteResult: (_h, o) => outcomes.push(o),
		onMessage: (cb) => {
			emit = cb;
			return () => {
				emit = () => {};
			};
		},
	};
	return {
		sent,
		outcomes,
		transport,
		emit: (m: Record<string, unknown>, handleId = "h1") => emit({ handleId, message: m }),
	};
}

/**
 * The identity mapping: `documentRoot === workspaceRoot`, which is every
 * language but Swift. Swift's mapping is tested where it lives, in
 * `lsp-uri.test.ts`.
 */
const createLspClient = (handleId: string, transport: Parameters<typeof createRawLspClient>[1]) =>
	createRawLspClient(handleId, transport, { workspaceRoot: "/w", documentRoot: "/w" });

describe("request", () => {
	test("resolves with the matching response", async () => {
		const h = harness();
		const client = createLspClient("h1", h.transport);
		const pending = client.request<{ ok: boolean }>("workspace/symbol", { query: "x" });
		h.emit({ jsonrpc: "2.0", id: h.sent[0].message.id, result: { ok: true } });
		await expect(pending).resolves.toEqual({ ok: true });
	});

	test("ignores a response addressed to a different handle", async () => {
		// One IPC channel carries every server's traffic, so a client that does not
		// filter would resolve on another workspace's answer.
		const h = harness();
		const client = createLspClient("h1", h.transport);
		const pending = client.request("workspace/symbol", { query: "x" });
		h.emit({ jsonrpc: "2.0", id: h.sent[0].message.id, result: ["wrong"] }, "h2");
		let settled = false;
		void pending.then(() => {
			settled = true;
		});
		await new Promise((r) => setTimeout(r, 20));
		expect(settled).toBe(false);
	});

	test("a server→client REQUEST never resolves a pending request that shares its id", async () => {
		// 🗝 Client ids and server ids are separate id spaces in JSON-RPC and they
		// overlap. Measured against real gopls: it sent
		// `window/workDoneProgress/create` with id 2 while our id 2 was in flight;
		// matching on id alone resolved our request with the server's request
		// payload AND left gopls waiting for an answer, so the workspace never
		// loaded and the server sat at 24 MB with no error anywhere.
		const h = harness();
		const client = createLspClient("h1", h.transport);
		const pending = client.request("workspace/symbol", { query: "x" });
		const ourId = h.sent[0].message.id as number;
		h.emit({ jsonrpc: "2.0", id: ourId, method: "window/workDoneProgress/create", params: { token: "load" } });
		let settled = false;
		void pending.then(() => {
			settled = true;
		});
		await new Promise((r) => setTimeout(r, 20));
		expect(settled).toBe(false);
		expect(h.outcomes).toEqual([]);

		// The real response still resolves it.
		h.emit({ jsonrpc: "2.0", id: ourId, result: [{ name: "X" }] });
		await expect(pending).resolves.toEqual([{ name: "X" }]);
	});

	test("rejects on a JSON-RPC error and reports it as `error`", async () => {
		const h = harness();
		const client = createLspClient("h1", h.transport);
		const pending = client.request("textDocument/definition", {});
		h.emit({ jsonrpc: "2.0", id: h.sent[0].message.id, error: { code: -32601, message: "nope" } });
		await expect(pending).rejects.toThrow(/nope/);
		expect(h.outcomes).toEqual(["error"]);
	});

	test("reports an empty array result as `empty`, a populated one as `ok`", async () => {
		// A server that is up, answering, and returning empty must be
		// DISTINGUISHABLE from one that is working. This is where that starts.
		const h = harness();
		const client = createLspClient("h1", h.transport);
		const a = client.request("workspace/symbol", { query: "x" });
		h.emit({ jsonrpc: "2.0", id: h.sent[0].message.id, result: [] });
		await a;
		const b = client.request("workspace/symbol", { query: "y" });
		h.emit({ jsonrpc: "2.0", id: h.sent[1].message.id, result: [{ name: "X" }] });
		await b;
		expect(h.outcomes).toEqual(["empty", "ok"]);
	});

	test("null and undefined results count as empty", async () => {
		const h = harness();
		const client = createLspClient("h1", h.transport);
		const p = client.request("textDocument/definition", {});
		h.emit({ jsonrpc: "2.0", id: h.sent[0].message.id, result: null });
		await p;
		expect(h.outcomes).toEqual(["empty"]);
	});
});

describe("the `opened` set", () => {
	test("didOpen is sent once per uri, didClose reopens the door", () => {
		const h = harness();
		const client = createLspClient("h1", h.transport);
		client.didOpen("file:///a.go", "go", "package a");
		client.didOpen("file:///a.go", "go", "package a");
		expect(h.sent.filter((s) => s.message.method === "textDocument/didOpen")).toHaveLength(1);
		client.didClose("file:///a.go");
		client.didOpen("file:///a.go", "go", "package a");
		expect(h.sent.filter((s) => s.message.method === "textDocument/didOpen")).toHaveLength(2);
	});

	test("a NEW client does not inherit the previous client's opened set", () => {
		// 🗝 THE SPIKE'S CARRIED BUG. Its `opened` set was permanent, so once a
		// server stopped, a previously-opened file never re-woke its language:
		// lspFor() short-circuited and the pane sat there with no intelligence and
		// no error. Here `opened` is per-client and a stopped server means a NEW
		// client, so it cannot recur - and this test says so out loud.
		const h = harness();
		const first = createLspClient("h1", h.transport);
		first.didOpen("file:///a.go", "go", "package a");
		expect(first.isOpen("file:///a.go")).toBe(true);
		first.dispose();

		const second = createLspClient("h2", h.transport);
		expect(second.isOpen("file:///a.go")).toBe(false);
		second.didOpen("file:///a.go", "go", "package a");
		expect(h.sent.filter((s) => s.message.method === "textDocument/didOpen")).toHaveLength(2);
	});
});

describe("dispose", () => {
	test("rejects everything still in flight rather than hanging forever", async () => {
		const h = harness();
		const client = createLspClient("h1", h.transport);
		const pending = client.request("workspace/symbol", { query: "x" });
		client.dispose();
		// A promise that never settles is a silent failure in promise form: the
		// palette would sit on "searching…" for the rest of the session.
		await expect(pending).rejects.toThrow(/disposed/i);
	});

	test("a request made after dispose rejects rather than vanishing", async () => {
		const h = harness();
		const client = createLspClient("h1", h.transport);
		client.dispose();
		await expect(client.request("workspace/symbol", {})).rejects.toThrow(/disposed/i);
	});

	test("unsubscribes from the transport", () => {
		const h = harness();
		const unsubscribe = vi.fn();
		const client = createLspClient("h1", { ...h.transport, onMessage: () => unsubscribe });
		client.dispose();
		expect(unsubscribe).toHaveBeenCalled();
	});
});

describe("onNotification", () => {
	/**
	 * 🗝 This door did not exist until the diagnostics slice, and its absence was
	 * invisible: `textDocument/publishDiagnostics` is UNSOLICITED — there is no
	 * pending request to leave hanging and no error to report — so every
	 * diagnostic both servers have ever sent this app was dropped on the floor
	 * while everything looked fine.
	 */
	test("a server notification reaches its listener", () => {
		const h = harness();
		const client = createLspClient("h1", h.transport);
		const seen: unknown[] = [];
		client.onNotification("textDocument/publishDiagnostics", (params) => seen.push(params));
		h.emit({ jsonrpc: "2.0", method: "textDocument/publishDiagnostics", params: { uri: "file:///a.go" } });
		expect(seen).toEqual([{ uri: "file:///a.go" }]);
	});

	test("only the method that was subscribed to", () => {
		const h = harness();
		const client = createLspClient("h1", h.transport);
		const seen: unknown[] = [];
		client.onNotification("textDocument/publishDiagnostics", (p) => seen.push(p));
		h.emit({ jsonrpc: "2.0", method: "window/logMessage", params: { message: "hi" } });
		expect(seen).toEqual([]);
	});

	// One IPC channel carries every server's traffic. Without the filter, a Go
	// workspace's diagnostics would be painted onto a Swift pane.
	test("a notification from ANOTHER handle is not delivered", () => {
		const h = harness();
		const client = createLspClient("h1", h.transport);
		const seen: unknown[] = [];
		client.onNotification("textDocument/publishDiagnostics", (p) => seen.push(p));
		h.emit({ jsonrpc: "2.0", method: "textDocument/publishDiagnostics", params: { uri: "x" } }, "h2");
		expect(seen).toEqual([]);
	});

	// 🗝 A server→client REQUEST carries a method AND an id, and MAIN answers it —
	// an unanswered one stalls a real server silently. Delivering it here as if it
	// were a notification would leave the renderer thinking it had been handled.
	test("a server→client REQUEST is not delivered as a notification", () => {
		const h = harness();
		const client = createLspClient("h1", h.transport);
		const seen: unknown[] = [];
		client.onNotification("workspace/configuration", (p) => seen.push(p));
		h.emit({ jsonrpc: "2.0", id: 7, method: "workspace/configuration", params: { items: [] } });
		expect(seen).toEqual([]);
	});

	test("unsubscribing stops delivery, and only for that listener", () => {
		const h = harness();
		const client = createLspClient("h1", h.transport);
		const a: unknown[] = [];
		const b: unknown[] = [];
		const off = client.onNotification("m", (p) => a.push(p));
		client.onNotification("m", (p) => b.push(p));
		off();
		h.emit({ jsonrpc: "2.0", method: "m", params: 1 });
		expect(a).toEqual([]);
		expect(b).toEqual([1]);
	});

	// The IPC callback is shared by every server in the window; one bad listener
	// must not take the channel down with it.
	test("one listener throwing still lets the others hear it", () => {
		const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
		const h = harness();
		const client = createLspClient("h1", h.transport);
		const seen: unknown[] = [];
		client.onNotification("m", () => {
			throw new Error("boom");
		});
		client.onNotification("m", (p) => seen.push(p));
		expect(() => h.emit({ jsonrpc: "2.0", method: "m", params: 2 })).not.toThrow();
		expect(seen).toEqual([2]);
		warn.mockRestore();
	});

	// A notification is not a result. Counting one would put a number in the
	// health panel's column that nobody asked a question to get.
	test("a notification is never counted as a request outcome", () => {
		const h = harness();
		const client = createLspClient("h1", h.transport);
		client.onNotification("m", () => undefined);
		h.emit({ jsonrpc: "2.0", method: "m", params: {} });
		expect(h.outcomes).toEqual([]);
	});

	test("a disposed client delivers nothing further", () => {
		const h = harness();
		const client = createLspClient("h1", h.transport);
		const seen: unknown[] = [];
		client.onNotification("m", (p) => seen.push(p));
		client.dispose();
		h.emit({ jsonrpc: "2.0", method: "m", params: {} });
		expect(seen).toEqual([]);
	});
});
