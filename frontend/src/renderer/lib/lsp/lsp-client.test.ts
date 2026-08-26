import { describe, expect, test, vi } from "vitest";
import { createLspClient, type LspTransport } from "./lsp-client";

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
