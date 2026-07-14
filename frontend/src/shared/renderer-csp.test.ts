import { describe, expect, it } from "vitest";
import { buildContentSecurityPolicy } from "./renderer-csp";

function directive(csp: string, name: string): string {
	const found = csp
		.split(";")
		.map((d) => d.trim())
		.find((d) => d === name || d.startsWith(name + " "));
	if (!found) throw new Error(`directive ${name} not present in CSP: ${csp}`);
	return found;
}

describe("buildContentSecurityPolicy", () => {
	// Regression guard for the app://renderer evidence-thumbnail bug: #111 fetched
	// the bytes and rendered a blob: object URL, but the CSP still forbade blob: in
	// img-src, so the <img> load was CSP-blocked and stayed a broken thumbnail.
	it("allows blob: (and data:) for images so evidence object URLs render", () => {
		const img = directive(buildContentSecurityPolicy(""), "img-src");
		expect(img).toContain("blob:");
		expect(img).toContain("data:");
		expect(img).toContain("'self'");
	});

	it("declares media-src allowing blob: so evidence video clips render", () => {
		const media = directive(buildContentSecurityPolicy(""), "media-src");
		expect(media).toContain("blob:");
		expect(media).toContain("'self'");
	});

	it("keeps the loopback daemon reachable via connect-src (fetch source of the bytes)", () => {
		const connect = directive(buildContentSecurityPolicy(""), "connect-src");
		expect(connect).toContain("http://127.0.0.1:*");
		expect(connect).toContain("ws://127.0.0.1:*");
	});

	it("does not widen loopback http into img-src/media-src (bytes flow only through fetch)", () => {
		const csp = buildContentSecurityPolicy("");
		expect(directive(csp, "img-src")).not.toContain("http://127.0.0.1");
		expect(directive(csp, "media-src")).not.toContain("http://127.0.0.1");
	});

	it("appends a configured PostHog origin to connect-src, and omits it when blank", () => {
		expect(directive(buildContentSecurityPolicy("https://ph.example"), "connect-src")).toContain("https://ph.example");
		// No trailing empty token when there is no PostHog origin.
		expect(buildContentSecurityPolicy("")).not.toMatch(/connect-src[^;]*\s;/);
	});
});
