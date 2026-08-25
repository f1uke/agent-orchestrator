import { setApiBaseUrl } from "../src/renderer/lib/api-client";
import { SWIFT_FIXTURE } from "./editor-fixture";

/**
 * Answers the one endpoint the workspace file viewer calls, so the harness page
 * stands on its own — `ao preview` can point at it with no daemon running, and
 * the spec needs no route interception.
 *
 * Two things are needed, not one: the client refuses to send at all until a
 * daemon base URL is known ("AO daemon is not ready"), and the request itself
 * has to be answered. So this points the client at the page's own origin and
 * then intercepts. Import it before anything that reaches `lib/api-client`.
 */
export const GALLERY_PATH = "Sources/PromotionHubViewController.swift";

export const GALLERY_CHANGED_LINES = [
	{ start: 15, end: 18, kind: "modified" },
	{ start: 21, end: 21, kind: "added" },
	{ start: 25, end: 25, kind: "removed" },
];

// Same origin: `runtimeFetch` short-circuits to the global `fetch` below.
setApiBaseUrl("");

const realFetch = window.fetch.bind(window);
window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
	const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
	if (!url.includes("/workspace/file")) return realFetch(input, init);
	const lines = SWIFT_FIXTURE.split("\n").map((text, i) => ({ kind: "context", oldLine: 0, newLine: i + 1, text }));
	return new Response(
		JSON.stringify({
			available: true,
			path: GALLERY_PATH,
			truncated: false,
			lines,
			changedLines: GALLERY_CHANGED_LINES,
		}),
		{ status: 200, headers: { "content-type": "application/json" } },
	);
};
