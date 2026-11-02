import { docTag } from "./chat-docs";
import { type ChatImage, type ImageMime, isImageMime } from "./chat-images";
import { IMAGE_EXT } from "./chat-refs";

export const MAX_IMAGES = 20;
export const MAX_IMAGE_BYTES = 5 * 1024 * 1024;
const LONG_EDGE = 2000;
const QUALITY = 0.9;

export type PreparedImage =
	{ ok: true; image: ChatImage; size: number } | { ok: false; reason: "too_large" | "unreadable" };

function toBase64(blob: Blob): Promise<string> {
	return new Promise((resolve, reject) => {
		const reader = new FileReader();
		reader.onload = () => {
			const url = String(reader.result);
			resolve(url.slice(url.indexOf(",") + 1));
		};
		reader.onerror = () => reject(reader.error ?? new Error("could not read the image"));
		reader.readAsDataURL(blob);
	});
}

/** The same picture at a 2000 px long edge: in its own type when the canvas can write it, else JPEG on white. */
async function shrink(bitmap: ImageBitmap, type: ImageMime): Promise<Blob | null> {
	const scale = LONG_EDGE / Math.max(bitmap.width, bitmap.height);
	const width = Math.round(bitmap.width * scale);
	const height = Math.round(bitmap.height * scale);
	const canvas = document.createElement("canvas");
	canvas.width = width;
	canvas.height = height;
	const context = canvas.getContext("2d");
	if (!context) return null;
	const encode = (as: ImageMime) => new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, as, QUALITY));
	// A canvas that cannot write a type answers with PNG, so a different type back means it could not.
	if (type !== "image/gif") {
		context.drawImage(bitmap, 0, 0, width, height);
		const blob = await encode(type);
		if (blob?.type === type) return blob;
	}
	context.fillStyle = "#fff";
	context.fillRect(0, 0, width, height);
	context.drawImage(bitmap, 0, 0, width, height);
	return encode("image/jpeg");
}

/** What the daemon takes of one image: at most 2000 px on the long edge and 5 MiB. */
export async function prepareImage(file: File): Promise<PreparedImage> {
	if (!isImageMime(file.type)) return { ok: false, reason: "unreadable" };
	let bitmap: ImageBitmap | undefined;
	try {
		if (typeof createImageBitmap === "function") bitmap = await createImageBitmap(file);
	} catch {
		return { ok: false, reason: "unreadable" };
	}
	try {
		const shrunk = bitmap && Math.max(bitmap.width, bitmap.height) > LONG_EDGE ? await shrink(bitmap, file.type) : file;
		if (!shrunk || !isImageMime(shrunk.type)) return { ok: false, reason: "unreadable" };
		if (shrunk.size > MAX_IMAGE_BYTES) return { ok: false, reason: "too_large" };
		return { ok: true, image: { mediaType: shrunk.type, base64: await toBase64(shrunk) }, size: shrunk.size };
	} catch {
		return { ok: false, reason: "unreadable" };
	} finally {
		bitmap?.close();
	}
}

const KIND_BY_EXTENSION: Record<string, Parameters<typeof docTag>[0]["kind"]> = {
	md: "markdown",
	markdown: "markdown",
	html: "html",
	htm: "html",
	pdf: "pdf",
	json: "json",
	txt: "text",
	log: "log",
	diff: "diff",
	patch: "diff",
	...Object.fromEntries(IMAGE_EXT.map((ext) => [ext, "image" as const])),
};

/** The tag on a file's chip: the document card's tag where it has one, else the extension in capitals. */
export function attachTag(name: string): string {
	const dot = name.lastIndexOf(".");
	const extension = dot > 0 ? name.slice(dot + 1).toLowerCase() : "";
	if (extension === "") return "FILE";
	const kind = KIND_BY_EXTENSION[extension];
	return kind ? docTag({ kind, path: name }) : extension.toUpperCase().slice(0, 4);
}

/** The text that goes to claude: what the person typed, then one `Attached:` line per uploaded file. */
export function withAttachedPaths(text: string, paths: readonly string[]): string {
	if (paths.length === 0) return text;
	const typed = text.trim() === "" ? [] : [text.trimEnd()];
	return [...typed, ...paths.map((path) => `Attached: ${path}`)].join("\n");
}
