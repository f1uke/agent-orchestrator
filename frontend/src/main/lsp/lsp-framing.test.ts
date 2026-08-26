import { describe, expect, test } from "vitest";
import { createFrameDecoder, encodeMessage } from "./lsp-framing";

describe("encodeMessage", () => {
	test("uses BYTE length, not character length", () => {
		// A header counting characters truncates the moment a message carries a
		// non-ASCII identifier or doc comment, and the server then reads the tail of
		// one message as the head of the next - which presents as a hang, not an error.
		const encoded = encodeMessage({ q: "héllo→" }).toString("utf8");
		const [header, body] = encoded.split("\r\n\r\n");
		expect(header).toBe(`Content-Length: ${Buffer.byteLength(body, "utf8")}`);
		expect(body).toBe(JSON.stringify({ q: "héllo→" }));
	});
});

describe("createFrameDecoder", () => {
	test("decodes two messages arriving in one chunk", () => {
		const d = createFrameDecoder();
		const chunk = Buffer.concat([encodeMessage({ id: 1 }), encodeMessage({ id: 2 })]);
		expect(d.push(chunk)).toEqual([{ id: 1 }, { id: 2 }]);
	});

	test("decodes one message split across arbitrary chunk boundaries", () => {
		const full = encodeMessage({ method: "initialize", params: { rootUri: "file:///r" } });
		for (const cut of [1, 5, 20, full.length - 1]) {
			const d = createFrameDecoder();
			expect(d.push(full.subarray(0, cut))).toEqual([]);
			expect(d.push(full.subarray(cut))).toEqual([{ method: "initialize", params: { rootUri: "file:///r" } }]);
		}
	});

	test("splits a multi-byte character across chunks without corrupting it", () => {
		// The bug this catches: decoding each chunk to a string before reassembling
		// turns a torn UTF-8 sequence into U+FFFD and the JSON no longer parses.
		const full = encodeMessage({ name: "配置" });
		const d = createFrameDecoder();
		const mid = full.length - 3;
		expect(d.push(full.subarray(0, mid))).toEqual([]);
		expect(d.push(full.subarray(mid))).toEqual([{ name: "配置" }]);
	});

	test("header matching is case-insensitive and tolerates extra headers", () => {
		const body = JSON.stringify({ ok: true });
		const raw = Buffer.from(
			`content-length: ${Buffer.byteLength(body)}\r\nContent-Type: application/vscode-jsonrpc\r\n\r\n${body}`,
			"utf8",
		);
		expect(createFrameDecoder().push(raw)).toEqual([{ ok: true }]);
	});

	test("a malformed body is dropped and the stream resynchronises", () => {
		const d = createFrameDecoder();
		const bad = Buffer.from("Content-Length: 3\r\n\r\n{{{", "utf8");
		expect(d.push(bad)).toEqual([]);
		expect(d.push(encodeMessage({ id: 7 }))).toEqual([{ id: 7 }]);
	});

	test("pendingBytes reports what is still buffered", () => {
		const d = createFrameDecoder();
		d.push(Buffer.from("Content-Length: 99\r\n\r\n", "utf8"));
		expect(d.pendingBytes).toBeGreaterThan(0);
	});
});
