import http from "node:http";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, describe, expect, test } from "vitest";
import { type NextEditRequest, nextEditPrompt, postNextEdit, rejoin } from "./next-edit";

const request = (over: Partial<NextEditRequest> = {}): NextEditRequest => ({
	path: "Sources/Greeter.swift",
	recent: [{ path: "Sources/Greeter.swift", original: "func greet() {", updated: "func greet(name: String) {" }],
	original: 'func greet() {\n\tprint("hi")\n}',
	current: 'func greet(name: String) {\n\tprint("hi")\n}',
	...over,
});

describe("nextEditPrompt", () => {
	test("is Sweep's training format: diffs, then original, current and an open updated block", () => {
		expect(nextEditPrompt(request())).toBe(
			[
				"<|file_sep|>Sources/Greeter.swift.diff",
				"original:",
				"func greet() {",
				"updated:",
				"func greet(name: String) {",
				"<|file_sep|>original/Sources/Greeter.swift",
				"func greet() {",
				'\tprint("hi")',
				"}",
				"<|file_sep|>current/Sources/Greeter.swift",
				"func greet(name: String) {",
				'\tprint("hi")',
				"}",
				"<|file_sep|>updated/Sources/Greeter.swift",
				"",
			].join("\n"),
		);
	});

	test("refuses text that would parse as one of the model's control tokens", () => {
		expect(nextEditPrompt(request({ current: 'let sep = "<|file_sep|>"' }))).toBeNull();
		expect(nextEditPrompt(request({ recent: [{ path: "a.ts", original: "<|endoftext|>", updated: "" }] }))).toBeNull();
		expect(nextEditPrompt(request({ path: "a\nb.ts" }))).toBeNull();
		// Look-alikes that are not tokens are fine.
		expect(nextEditPrompt(request({ current: "a <| b |> c" }))).not.toBeNull();
	});
});

describe("rejoin", () => {
	const current = ["a", "b", "c", "d", "e", "f"].join("\n");

	test("waits while the rewrite is still copying the window", () => {
		expect(rejoin("a\nb\n", current)).toBeNull();
	});

	test("waits until enough lines after a change match the window again", () => {
		expect(rejoin("a\nB\n", current)).toBeNull();
		expect(rejoin("a\nB\nc\n", current)).toBeNull();
	});

	test("splices the rest of the window in once the rewrite has rejoined it", () => {
		expect(rejoin("a\nB\nc\nd\n", current)).toBe(["a", "B", "c", "d", "e", "f"].join("\n"));
	});

	test("rejoins after inserted lines, at the matching place", () => {
		expect(rejoin("a\nb\nNEW\nc\nd\n", current)).toBe(["a", "b", "NEW", "c", "d", "e", "f"].join("\n"));
	});

	test("ignores the line still being generated", () => {
		expect(rejoin("a\nB\nc\nd", current)).toBeNull();
	});

	test("never rejoins on blank lines alone", () => {
		const gappy = ["a", "", "", "b", "c"].join("\n");
		expect(rejoin("X\n\n\n", gappy)).toBeNull();
	});
});

describe("postNextEdit", () => {
	let dir: string;
	let server: http.Server | null = null;
	afterEach(() => {
		server?.close();
		server = null;
		if (dir) rmSync(dir, { recursive: true, force: true });
	});

	/** A server that streams `pieces` as llama-server's SSE events, and records what it saw. */
	async function streaming(pieces: string[], stopType = "eos") {
		dir = mkdtempSync(path.join(tmpdir(), "ne-"));
		const socketPath = path.join(dir, "s.sock");
		const seen = { body: null as Record<string, unknown> | null, closedEarly: false };
		server = http.createServer((req, res) => {
			let raw = "";
			req.on("data", (d) => (raw += d));
			req.on("end", async () => {
				seen.body = JSON.parse(raw);
				res.writeHead(200, { "content-type": "text/event-stream" });
				res.on("close", () => {
					if (!res.writableEnded) seen.closedEarly = true;
				});
				for (const content of pieces) {
					if (res.destroyed) return;
					res.write(`data: ${JSON.stringify({ content, stop: false })}\n\n`);
					await new Promise((r) => setTimeout(r, 5));
				}
				res.end(
					`data: ${JSON.stringify({ content: "", stop: true, stop_type: stopType, timings: { prompt_n: 10, prompt_ms: 2, predicted_n: 7, predicted_ms: 30 } })}\n\n`,
				);
			});
		});
		await new Promise<void>((r) => server?.listen(socketPath, r));
		return { socketPath, seen };
	}

	test("streams the rewrite and returns the whole window", async () => {
		const { socketPath, seen } = await streaming(["func greet(name: String) {\n", '\tprint("hi, \\(name)")\n', "}\n"]);
		const result = await postNextEdit(socketPath, request(), new AbortController().signal);
		expect(result?.window).toBe('func greet(name: String) {\n\tprint("hi, \\(name)")\n}');
		expect(result?.early).toBe(false);
		expect(result?.predictedTokens).toBe(7);
		expect(seen.body).toMatchObject({ temperature: 0, stream: true, stop: ["<|file_sep|>", "</s>"] });
	});

	test("stops reading once the rewrite rejoins the window, and closes the stream", async () => {
		const current = ["a", "b", "c", "d", "e", "f", "g"].join("\n");
		const { socketPath, seen } = await streaming(["a\n", "B\n", "c\n", "d\n", "e\n", "f\n", "g\n"]);
		const result = await postNextEdit(socketPath, request({ current }), new AbortController().signal);
		expect(result?.window).toBe(["a", "B", "c", "d", "e", "f", "g"].join("\n"));
		expect(result?.early).toBe(true);
		await new Promise((r) => setTimeout(r, 30));
		expect(seen.closedEarly).toBe(true);
	});

	test("a rewrite cut off by the token limit is no answer, not a window missing its end", async () => {
		const { socketPath } = await streaming(["func greet(name: String) {\n", '\tprint("hi")\n'], "limit");
		expect(await postNextEdit(socketPath, request(), new AbortController().signal)).toBeNull();
	});

	test("answers null without asking when the prompt cannot be built", async () => {
		const { socketPath, seen } = await streaming([]);
		expect(
			await postNextEdit(socketPath, request({ current: "<|file_sep|>" }), new AbortController().signal),
		).toBeNull();
		expect(seen.body).toBeNull();
	});
});
