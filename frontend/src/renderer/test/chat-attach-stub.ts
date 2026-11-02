import { vi } from "vitest";

/**
 * Stands in for the browser's image decoder and canvas, which jsdom lacks. `encodes` lists the
 * types the canvas can write; any other type comes out as PNG, as a real canvas does.
 */
export function stubImageDecoder(
	width: number,
	height: number,
	encodes: string[] = ["image/png", "image/jpeg", "image/webp"],
) {
	const close = vi.fn();
	vi.stubGlobal(
		"createImageBitmap",
		vi.fn(async () => ({ width, height, close })),
	);
	const drawn: number[][] = [];
	const filled: string[] = [];
	const encoded: Array<{ type?: string; quality?: unknown }> = [];
	const context = {
		fillStyle: "",
		fillRect: () => filled.push(context.fillStyle),
		drawImage: (_bitmap: unknown, ...rect: number[]) => drawn.push(rect),
	};
	vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(context as never);
	vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation(function (cb, type, quality) {
		encoded.push({ type, quality });
		const wrote = type && encodes.includes(type) ? type : "image/png";
		cb(new Blob([new Uint8Array([1, 2, 3])], { type: wrote }));
	});
	return { drawn, filled, encoded, close };
}
