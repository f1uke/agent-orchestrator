import { setApiBaseUrl } from "../src/renderer/lib/api-client";
import { fixtureWithLabel, SWIFT_FIXTURE } from "./editor-fixture";

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
export const GALLERY_PATH =
	new URLSearchParams(window.location.search).get("path") ?? "Sources/PromotionHubViewController.swift";

export const GALLERY_CHANGED_LINES = [
	{ start: 15, end: 18, kind: "modified" },
	{ start: 21, end: 21, kind: "added" },
	{ start: 25, end: 25, kind: "removed" },
];

// Same origin: `runtimeFetch` short-circuits to the global `fetch` below.
setApiBaseUrl("");

// `?label=` swaps in a fixture carrying one extra section marker with that
// label, so a spec can find the width at which the minimap starts truncating a
// longer, realistic section name.
const labelParam = new URLSearchParams(window.location.search).get("label");
const SOURCE = labelParam ? fixtureWithLabel(labelParam) : SWIFT_FIXTURE;

/**
 * 🗝 Registered BEFORE fetch is patched, and it is the half that actually works
 * now: under `dev:web` (VITE_NO_ELECTRON=1) the viewer serves mock data and
 * never issues a request, so an interception-only stub would hand the gallery
 * `lib/mock-data`'s generic fixture instead of the Swift one — no `// MARK:`
 * sections, and every minimap assertion in `editor.spec.ts` timing out with
 * nothing in the console. The fetch patch below stays for the non-preview build.
 */
(globalThis as { __aoMockWorkspaceFile?: unknown }).__aoMockWorkspaceFile = {
	file(path: string) {
		if (path !== GALLERY_PATH) return null;
		return {
			available: true,
			path: GALLERY_PATH,
			truncated: false,
			trailingNewline: true,
			contentHash: "sha256:gallery",
			lines: SOURCE.split("\n").map((text, i) => ({ kind: "context", oldLine: 0, newLine: i + 1, text })),
			changedLines: GALLERY_CHANGED_LINES,
		};
	},
	// No branch-level diff: this harness measures the minimap, and a diff built
	// from a DIFFERENT file's text would mark lanes on lines that do not match.
	diff: () => ({ available: false, truncated: false, mode: "file", path: GALLERY_PATH, lines: [] }),
};

const realFetch = window.fetch.bind(window);
window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
	const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
	if (!url.includes("/workspace/file")) return realFetch(input, init);
	const lines = SOURCE.split("\n").map((text, i) => ({ kind: "context", oldLine: 0, newLine: i + 1, text }));
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
