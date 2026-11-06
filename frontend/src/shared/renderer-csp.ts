// Content-Security-Policy for the built renderer, kept as a single source of
// truth shared by the Vite build (which injects it as a <meta> tag at build
// time) and its unit test. The daemon is loopback-only, so network access is
// pinned to 127.0.0.1 (REST + SSE over http, terminal mux over ws).
//
// `blob:` in img-src/media-src is required for the Jira media previews, which
// fetch the bytes (connect-src) and render them from an object URL
// (`blob:app://renderer/…`); without `blob:` here the element load is
// CSP-blocked and shows a broken thumbnail, the bug that survived #111.
//
// Loopback http is also in img-src/media-src so <img> and <video> can point at the daemon and a video
// streams by Range request instead of being read whole into a blob.
//
// `frame-src 'self'` is for the chat widget frame on /widget-frame.html; srcdoc, data: and blob:
// would inherit `script-src 'self'` and block the widget's inline scripts.
export function buildContentSecurityPolicy(posthogOrigin: string): string {
	return [
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: http://127.0.0.1:*",
		"media-src 'self' blob: http://127.0.0.1:*",
		"font-src 'self' data:",
		["connect-src", "'self'", "http://127.0.0.1:*", "ws://127.0.0.1:*", posthogOrigin].filter(Boolean).join(" "),
		"object-src 'none'",
		"base-uri 'self'",
		"frame-src 'self'",
	].join("; ");
}
