// Re-checked here, not only in the renderer's safeHref: the renderer shows
// untrusted agent text, so the main process must not trust its links.
const SYSTEM_BROWSER_SCHEMES = new Set(["http:", "https:", "mailto:"]);

export function opensInSystemBrowser(url: string): boolean {
	try {
		return SYSTEM_BROWSER_SCHEMES.has(new URL(url).protocol);
	} catch {
		return false;
	}
}
