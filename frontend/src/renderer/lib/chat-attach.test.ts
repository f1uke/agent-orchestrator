import { afterEach, describe, expect, it, vi } from "vitest";
import { stubImageDecoder } from "../test/chat-attach-stub";
import { attachTag, prepareImage, withAttachedPaths } from "./chat-attach";

const MIB = 1024 * 1024;

const picture = (bytes = 4, name = "shot.png", type = "image/png") =>
	new File([new Uint8Array(bytes).fill(7)], name, { type });

afterEach(() => {
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

describe("prepareImage", () => {
	it("keeps a small image as it is", async () => {
		const seen = stubImageDecoder(1200, 800);
		const result = await prepareImage(picture(4));
		expect(result).toEqual({ ok: true, image: { mediaType: "image/png", base64: "BwcHBw==" }, size: 4 });
		expect(seen.drawn).toEqual([]);
		expect(seen.close).toHaveBeenCalled();
	});

	it("shrinks the long edge to 2000 px and keeps a type the canvas can write", async () => {
		const seen = stubImageDecoder(4000, 3000);
		const result = await prepareImage(picture(4));
		expect(seen.drawn).toEqual([[0, 0, 2000, 1500]]);
		expect(seen.encoded.map((e) => e.type)).toEqual(["image/png"]);
		expect(result).toEqual({ ok: true, image: { mediaType: "image/png", base64: "AQID" }, size: 3 });
	});

	it("shrinks a tall image by its height", async () => {
		const seen = stubImageDecoder(1000, 5000);
		await prepareImage(picture(4, "tall.png"));
		expect(seen.drawn).toEqual([[0, 0, 400, 2000]]);
	});

	it("writes a gif that must shrink as JPEG at 0.9 on a white ground", async () => {
		const seen = stubImageDecoder(3000, 3000);
		const result = await prepareImage(picture(4, "loop.gif", "image/gif"));
		expect(seen.encoded).toEqual([{ type: "image/jpeg", quality: 0.9 }]);
		expect(seen.filled).toEqual(["#fff"]);
		expect(result).toMatchObject({ ok: true, image: { mediaType: "image/jpeg" } });
	});

	it("falls back to JPEG when the canvas cannot write the type", async () => {
		const seen = stubImageDecoder(3000, 3000, ["image/jpeg"]);
		const result = await prepareImage(picture(4, "pic.webp", "image/webp"));
		expect(seen.encoded.map((e) => e.type)).toEqual(["image/webp", "image/jpeg"]);
		expect(result).toMatchObject({ ok: true, image: { mediaType: "image/jpeg" } });
	});

	it("refuses an image over 5 MiB that does not need shrinking", async () => {
		stubImageDecoder(1000, 1000);
		expect(await prepareImage(picture(5 * MIB + 1))).toEqual({ ok: false, reason: "too_large" });
		expect(await prepareImage(picture(5 * MIB))).toMatchObject({ ok: true });
	});

	it("refuses an image that is still over 5 MiB after shrinking", async () => {
		stubImageDecoder(4000, 4000);
		vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation((cb, type) =>
			cb(new Blob([new Uint8Array(5 * MIB + 1)], { type: type ?? "image/png" })),
		);
		expect(await prepareImage(picture(4))).toEqual({ ok: false, reason: "too_large" });
	});

	it("refuses a file the decoder rejects", async () => {
		vi.stubGlobal(
			"createImageBitmap",
			vi.fn(async () => {
				throw new DOMException("bad image", "InvalidStateError");
			}),
		);
		expect(await prepareImage(picture(4))).toEqual({ ok: false, reason: "unreadable" });
	});

	it("passes the bytes through where the browser has no decoder", async () => {
		expect(await prepareImage(picture(4))).toEqual({
			ok: true,
			image: { mediaType: "image/png", base64: "BwcHBw==" },
			size: 4,
		});
	});
});

describe("attachTag", () => {
	it.each([
		["report.pdf", "PDF"],
		["data.json", "JSON"],
		["notes.md", "MD"],
		["run.log", "LOG"],
		["fix.patch", "DIFF"],
		["table.csv", "CSV"],
		["Archive.TAR.GZ", "GZ"],
		["Makefile", "FILE"],
		["x.xcresult", "XCRE"],
	])("tags %s as %s", (name, tag) => {
		expect(attachTag(name)).toBe(tag);
	});
});

describe("withAttachedPaths", () => {
	it("leaves the text alone when nothing is attached", () => {
		expect(withAttachedPaths("hi\n", [])).toBe("hi\n");
	});

	it("adds one Attached line per file after the text", () => {
		expect(withAttachedPaths("Read these \n", ["/d/a/x.pdf", "/d/b/y.json"])).toBe(
			"Read these\nAttached: /d/a/x.pdf\nAttached: /d/b/y.json",
		);
	});

	it("sends only the lines when the text is empty", () => {
		expect(withAttachedPaths("  ", ["/d/a/x.pdf"])).toBe("Attached: /d/a/x.pdf");
	});
});
