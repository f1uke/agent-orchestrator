// A link a renderer window tries to open (a chat or note link with
// target="_blank") goes to the OS default handler only for these schemes. The
// renderer's safeHref draws every other scheme as text, and this is the same
// list again in the main process, because the renderer shows untrusted agent
// text.
const SYSTEM_BROWSER_SCHEMES = new Set(["http:", "https:", "mailto:"]);

export function opensInSystemBrowser(url: string): boolean {
	try {
		return SYSTEM_BROWSER_SCHEMES.has(new URL(url).protocol);
	} catch {
		return false;
	}
}
