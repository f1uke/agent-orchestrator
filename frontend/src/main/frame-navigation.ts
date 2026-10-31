/** The only page the chat's widget frames load. It is a static file the renderer build copies from public/. */
export const WIDGET_FRAME_PATH = "/widget-frame.html";

/**
 * Whether a subframe of the main window may navigate to `url`. A widget's script runs in a frame that
 * can set `location` to any host, so every subframe navigation is refused except to the widget frame
 * page on the app's own origin (or the blank page a frame starts on).
 */
export function allowSubframeNavigation(url: string, appUrl: string): boolean {
	if (url === "about:blank" || url === "about:srcdoc") return true;
	try {
		const target = new URL(url);
		const app = new URL(appUrl);
		return target.protocol === app.protocol && target.host === app.host && target.pathname === WIDGET_FRAME_PATH;
	} catch {
		return false;
	}
}
