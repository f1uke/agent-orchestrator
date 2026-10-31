const CRC_TABLE = Array.from({ length: 256 }, (_, n) => {
	let c = n;
	for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
	return c >>> 0;
});

function crc32(bytes: Uint8Array): number {
	let c = 0xffffffff;
	for (const b of bytes) c = CRC_TABLE[(c ^ b) & 0xff] ^ (c >>> 8);
	return (c ^ 0xffffffff) >>> 0;
}

function adler32(bytes: Uint8Array): number {
	let a = 1;
	let b = 0;
	for (const v of bytes) {
		a = (a + v) % 65521;
		b = (b + a) % 65521;
	}
	return ((b << 16) | a) >>> 0;
}

const u32 = (n: number) => [(n >>> 24) & 255, (n >>> 16) & 255, (n >>> 8) & 255, n & 255];

function chunk(type: string, data: number[] | Uint8Array): number[] {
	const body = Uint8Array.from([...[...type].map((c) => c.charCodeAt(0)), ...data]);
	return [...u32(data.length), ...body, ...u32(crc32(body))];
}

/** zlib stream of stored (uncompressed) deflate blocks: no compressor needed for a fictional test image. */
function zlibStored(raw: Uint8Array): number[] {
	const out: number[] = [0x78, 0x01];
	for (let at = 0; at < raw.length; at += 65535) {
		const part = raw.subarray(at, Math.min(at + 65535, raw.length));
		const last = at + part.length >= raw.length ? 1 : 0;
		out.push(last, part.length & 255, part.length >> 8, ~part.length & 255, (~part.length >> 8) & 255);
		for (const v of part) out.push(v);
	}
	return [...out, ...u32(adler32(raw))];
}

export type Rgb = [number, number, number];

/** A PNG of `width` x `height` pixels, each coloured by `pixel(x, y)`, as base64. */
export function pngBase64(width: number, height: number, pixel: (x: number, y: number) => Rgb): string {
	const raw = new Uint8Array(height * (1 + width * 3));
	let at = 0;
	for (let y = 0; y < height; y++) {
		raw[at++] = 0;
		for (let x = 0; x < width; x++) for (const v of pixel(x, y)) raw[at++] = v;
	}
	const png = Uint8Array.from([
		137,
		80,
		78,
		71,
		13,
		10,
		26,
		10,
		...chunk("IHDR", [...u32(width), ...u32(height), 8, 2, 0, 0, 0]),
		...chunk("IDAT", zlibStored(raw)),
		...chunk("IEND", []),
	]);
	let binary = "";
	for (const b of png) binary += String.fromCharCode(b);
	return btoa(binary);
}
