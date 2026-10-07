/** Whether a MIME type is a video (rendered with <video>, not <img>). */
export function isVideoMime(mime: string): boolean {
	return mime.startsWith("video/");
}
