// Content-Security-Policy for the built renderer, kept as a single source of
// truth shared by the Vite build (which injects it as a <meta> tag at build
// time) and its unit test. The daemon is loopback-only, so network access is
// pinned to 127.0.0.1 (REST + SSE over http, terminal mux over ws).
//
// `blob:` in img-src/media-src is required for smoke-test evidence previews.
// The renderer runs on the secure `app://` scheme, where a direct
// <img>/<video src=http://127.0.0.1…> subresource is CSP-blocked (loopback http
// lives only in connect-src). So the evidence bytes are fetched (connect-src)
// and rendered from an object URL (`blob:app://renderer/…`); without `blob:`
// here the element load is CSP-blocked and shows a broken thumbnail — the bug
// that survived #111 (which added the blob fetch but not this CSP allowance).
export function buildContentSecurityPolicy(posthogOrigin: string): string {
	return [
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"media-src 'self' blob:",
		"font-src 'self' data:",
		["connect-src", "'self'", "http://127.0.0.1:*", "ws://127.0.0.1:*", posthogOrigin].filter(Boolean).join(" "),
		"object-src 'none'",
		"base-uri 'self'",
		"frame-src 'none'",
	].join("; ");
}
