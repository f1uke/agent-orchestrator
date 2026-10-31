import { describe, expect, it } from "vitest";
import { parseEvent } from "./chat-event";

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
});
