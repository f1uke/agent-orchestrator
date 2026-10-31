import { describe, expect, it } from "vitest";
import { imageSrc, imagesOf } from "./chat-images";

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

	it.each([["a string"], [undefined], [null], [{ type: "image" }], [42]])("returns nothing for %j", (content) => {
		expect(imagesOf(content)).toEqual([]);
	});
});

describe("imageSrc", () => {
	it("builds a data URL the renderer CSP allows without a fetch", () => {
		expect(imageSrc({ mediaType: "image/jpeg", base64: "QUJD" })).toBe("data:image/jpeg;base64,QUJD");
	});
});
