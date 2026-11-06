import { describe, expect, it } from "vitest";
import { chatBlobUrl, imageSrc, imagesOf } from "./chat-images";

const block = (media_type: string, data = "AAAA") => ({ type: "image", source: { type: "base64", media_type, data } });

describe("imagesOf", () => {
	it.each(["image/png", "image/jpeg", "image/gif", "image/webp"])("keeps a %s base64 block with its bytes", (type) => {
		expect(imagesOf([block(type, "QUJD")])).toEqual([{ mediaType: type, base64: "QUJD" }]);
	});

	it("keeps the images of a mixed result in order and drops everything else", () => {
		const content = [
			{ type: "text", text: "caption" },
			block("image/png", "one"),
			{ type: "image", source: { type: "url", url: "https://example.test/a.png" } },
			block("image/svg+xml", "svg"),
			block("image/png", ""),
			block("image/webp", "two"),
			null,
			"stray",
		];
		expect(imagesOf(content)).toEqual([
			{ mediaType: "image/png", base64: "one" },
			{ mediaType: "image/webp", base64: "two" },
		]);
	});

	it("keeps an image the daemon stores by reference as its blob name", () => {
		const name = "8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4.png";
		const ref = { type: "image", source: { type: "ao_blob", media_type: "image/png", blob: name, size: 2 } };
		expect(imagesOf([ref, { type: "image", source: { type: "ao_blob", media_type: "image/png", blob: "" } }])).toEqual([
			{ mediaType: "image/png", blob: name },
		]);
	});

	it.each([["a string"], [undefined], [null], [{ type: "image" }], [42]])("returns nothing for %j", (content) => {
		expect(imagesOf(content)).toEqual([]);
	});
});

describe("imageSrc", () => {
	it("builds a data URL the renderer CSP allows without a fetch", () => {
		expect(imageSrc({ mediaType: "image/jpeg", base64: "QUJD" })).toBe("data:image/jpeg;base64,QUJD");
	});
});

describe("chatBlobUrl", () => {
	it("names the session's blob route, escaping the session id", () => {
		expect(chatBlobUrl("http://127.0.0.1:4317", "ao 1", "ab.png")).toBe(
			"http://127.0.0.1:4317/api/v1/sessions/ao%201/chat/blobs/ab.png",
		);
	});
});
