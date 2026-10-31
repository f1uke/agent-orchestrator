import { describe, expect, it } from "vitest";
import { allowSubframeNavigation } from "./frame-navigation";

const PACKAGED = "app://renderer/index.html";
const DEV = "http://localhost:5173/";

describe("allowSubframeNavigation", () => {
	it("lets a widget frame load its page from the app's own origin, packaged or in dev", () => {
		expect(allowSubframeNavigation("app://renderer/widget-frame.html", PACKAGED)).toBe(true);
		expect(allowSubframeNavigation("http://localhost:5173/widget-frame.html", DEV)).toBe(true);
	});

	it("lets a frame sit on the blank page", () => {
		expect(allowSubframeNavigation("about:blank", PACKAGED)).toBe(true);
		expect(allowSubframeNavigation("about:srcdoc", PACKAGED)).toBe(true);
	});

	it("refuses a frame sent to another host, the loopback daemon, another app page or another scheme", () => {
		for (const url of [
			"https://evil.example/widget-frame.html",
			"http://127.0.0.1:3001/api/v1/sessions",
			"app://renderer/index.html",
			"app://other/widget-frame.html",
			"http://localhost:5174/widget-frame.html",
			"file:///tmp/widget-frame.html",
			"javascript:alert(1)",
			"data:text/html,<p>x</p>",
			"not a url",
			"",
		]) {
			expect(allowSubframeNavigation(url, PACKAGED), url).toBe(false);
		}
		expect(allowSubframeNavigation("app://renderer/widget-frame.html", DEV)).toBe(false);
	});
});
