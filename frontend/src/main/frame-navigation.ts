export const WIDGET_FRAME_PATH = "/widget-frame.html";

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
