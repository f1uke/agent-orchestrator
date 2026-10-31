import { isObject } from "./chat-event";

export type ImageMime = "image/png" | "image/jpeg" | "image/gif" | "image/webp";

export type ChatImage = { mediaType: ImageMime; base64: string };

const IMAGE_MIMES: ReadonlySet<string> = new Set<ImageMime>(["image/png", "image/jpeg", "image/gif", "image/webp"]);

export function isImageMime(mediaType: string): mediaType is ImageMime {
	return IMAGE_MIMES.has(mediaType);
}

export function imagesOf(content: unknown): ChatImage[] {
	if (!Array.isArray(content)) return [];
	const images: ChatImage[] = [];
	for (const block of content) {
		if (!isObject(block) || block.type !== "image" || !isObject(block.source)) continue;
		const { type, media_type: mediaType, data } = block.source;
		if (
			type === "base64" &&
			typeof mediaType === "string" &&
			isImageMime(mediaType) &&
			typeof data === "string" &&
			data !== ""
		) {
			images.push({ mediaType, base64: data });
		}
	}
	return images;
}

export function imageSrc(image: ChatImage): string {
	return `data:${image.mediaType};base64,${image.base64}`;
}
