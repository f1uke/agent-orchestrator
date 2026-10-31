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
});
