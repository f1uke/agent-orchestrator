import { describe, expect, it } from "vitest";
import { parseEvent, parseHostNote } from "./chat-event";

describe("parseEvent", () => {
	it("keeps the bytes of a user image block", () => {
		const event = parseEvent({
			type: "user",
			uuid: "u1",
			message: {
				role: "user",
				content: [
					{ type: "text", text: "look" },
					{ type: "image", source: { type: "base64", media_type: "image/png", data: "iVBORw==" } },
				],
			},
		});
		expect(event).toMatchObject({
			type: "user",
			blocks: [
				{ type: "text", text: "look" },
				{ type: "image", mediaType: "image/png", data: "iVBORw==" },
			],
		});
	});

	it("keeps the blob name of an image the daemon stores by reference", () => {
		const event = parseEvent({
			type: "user",
			message: {
				content: [{ type: "image", source: { type: "ao_blob", media_type: "image/jpeg", blob: "ab.jpg", size: 9 } }],
			},
		});
		expect(event).toMatchObject({ blocks: [{ type: "image", mediaType: "image/jpeg", data: "", blob: "ab.jpg" }] });
	});

	it("gives an image with a non-base64 source no bytes", () => {
		const event = parseEvent({
			type: "user",
			message: { content: [{ type: "image", source: { type: "url", url: "https://example.test/a.png" } }] },
		});
		expect(event).toMatchObject({ blocks: [{ type: "image", mediaType: "", data: "" }] });
	});

	describe("tool_use_result", () => {
		const hunk = { oldStart: 456, oldLines: 2, newStart: 456, newLines: 2, lines: [" keep", "-old", "+new"] };
		const user = (toolUseResult: unknown) =>
			parseEvent({
				type: "user",
				message: { content: [{ type: "tool_result", tool_use_id: "t1", content: "ok" }] },
				tool_use_result: toolUseResult,
			});

		it("keeps the structuredPatch of an Edit result", () => {
			expect(user({ filePath: "/a.go", structuredPatch: [hunk] })).toMatchObject({ structuredPatch: [hunk] });
		});

		it("never keeps the file text that rides along with the patch", () => {
			const event = user({
				originalFile: "SECRET ORIGINAL TEXT",
				content: "SECRET CONTENT",
				file: { content: "SECRET FILE" },
				structuredPatch: [hunk],
			});
			expect(JSON.stringify(event)).not.toContain("SECRET");
		});

		it("drops a hunk that is not shaped like one", () => {
			const event = user({
				structuredPatch: [hunk, { oldStart: "x", lines: [] }, null, { ...hunk, lines: [1, " ok"] }],
			});
			expect(event).toMatchObject({ structuredPatch: [hunk, { ...hunk, lines: [" ok"] }] });
		});

		it("has no patch for a result without one", () => {
			const patchOf = (event: unknown) => (event as { structuredPatch?: unknown }).structuredPatch;
			expect(patchOf(user({ type: "text", file: { content: "x" } }))).toBeUndefined();
			expect(patchOf(user({ structuredPatch: [] }))).toBeUndefined();
			expect(patchOf(user(undefined))).toBeUndefined();
		});
	});

	describe("prompt_suggestion", () => {
		it("reads the suggested next message", () => {
			expect(
				parseEvent({
					type: "prompt_suggestion",
					suggestion: "what happens if I forget to cancel it?",
					uuid: "24f24088-0c81-468b-97eb-3bfbaac2f6b9",
					session_id: "88b935b5-28d7-433b-a75a-bd12d380a5f1",
				}),
			).toEqual({ type: "prompt_suggestion", text: "what happens if I forget to cancel it?" });
		});

		it("degrades to unknown when the suggestion is not text", () => {
			expect(parseEvent({ type: "prompt_suggestion", suggestion: 7 })).toEqual({
				type: "unknown",
				eventType: "prompt_suggestion",
			});
		});
	});
});

describe("parseHostNote bash notes", () => {
	it("reads a start, an output chunk and an end", () => {
		expect(parseHostNote({ kind: "bash_start", id: "b1", command: "ls", cwd: "/w" })).toEqual({
			kind: "bash_start",
			id: "b1",
			command: "ls",
			cwd: "/w",
		});
		expect(parseHostNote({ kind: "bash_output", id: "b1", stream: "stderr", text: "e" })).toEqual({
			kind: "bash_output",
			id: "b1",
			stream: "stderr",
			text: "e",
		});
		expect(
			parseHostNote({ kind: "bash_end", id: "b1", code: 3, signal: null, stopped: false, truncated: true }),
		).toEqual({ kind: "bash_end", id: "b1", code: 3, signal: null, stopped: false, truncated: true });
	});

	it("reads an unknown stream as stdout and a missing code or signal as null", () => {
		expect(parseHostNote({ kind: "bash_output", id: "b1", stream: "tty", text: "x" })).toMatchObject({
			stream: "stdout",
		});
		expect(parseHostNote({ kind: "bash_end", id: "b1", stopped: true })).toEqual({
			kind: "bash_end",
			id: "b1",
			code: null,
			signal: null,
			stopped: true,
			truncated: false,
		});
	});
});

describe("parseEvent shouldQuery", () => {
	const user = (extra: object) =>
		parseEvent({ type: "user", uuid: "u1", message: { role: "user", content: "x" }, ...extra });

	it("is true unless the message says false", () => {
		expect(user({})).toMatchObject({ shouldQuery: true });
		expect(user({ shouldQuery: false })).toMatchObject({ shouldQuery: false });
	});
});
