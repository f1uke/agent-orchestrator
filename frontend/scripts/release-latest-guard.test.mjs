// @vitest-environment node
import { describe, it, expect } from "vitest";
import { REQUIRED_ASSETS, parseGhApiResponse, evaluateLatestRelease } from "./release-latest-guard.mjs";

const assets = REQUIRED_ASSETS.map((name) => ({ name }));

// Verbatim shape of `gh api --include`: LF-terminated status line, CRLF headers,
// blank CRLF line, then the JSON body.
function ghResponse(statusLine, body) {
	return `${statusLine}\nContent-Type: application/json\r\n\r\n${JSON.stringify(body)}`;
}

describe("parseGhApiResponse", () => {
	it("splits the status code from the JSON body", () => {
		const raw = ghResponse("HTTP/2.0 200 OK", { tagName: "v0.10.2" });
		expect(parseGhApiResponse(raw)).toEqual({ status: 200, body: { tagName: "v0.10.2" } });
	});

	it("reads a 404 status even though the body is an error envelope", () => {
		const raw = ghResponse("HTTP/2.0 404 Not Found", { message: "Not Found", status: "404" });
		expect(parseGhApiResponse(raw).status).toBe(404);
	});

	// A transport failure (DNS/TLS/proxy) makes gh print nothing on stdout, so
	// there is no status line to trust. That must never look like a 404.
	it("throws when there is no HTTP status line at all", () => {
		expect(() => parseGhApiResponse("")).toThrow(/no HTTP status line/i);
		expect(() => parseGhApiResponse("error connecting to github.com\n")).toThrow(/no HTTP status line/i);
	});

	it("throws when the body is not JSON", () => {
		expect(() => parseGhApiResponse("HTTP/2.0 200 OK\r\n\r\n<html>nope</html>")).toThrow();
	});
});

describe("evaluateLatestRelease", () => {
	const stable = {
		tag_name: "v0.10.2",
		draft: false,
		prerelease: false,
		assets,
	};

	// Case A - the bug this guard fix is for. /releases/latest ignores
	// prereleases, so a repo with only nightlies 404s. That is expected state,
	// not a failure.
	it("skips with a warning when no stable release exists yet (404)", () => {
		const result = evaluateLatestRelease({ status: 404, body: { message: "Not Found" } });
		expect(result.outcome).toBe("skip");
		expect(result.message).toMatch(/no stable github release exists yet/i);
	});

	// Case B
	it("passes a stable release that carries every updater feed asset", () => {
		expect(evaluateLatestRelease({ status: 200, body: stable })).toEqual({
			outcome: "pass",
			message: expect.stringContaining("v0.10.2"),
		});
	});

	// Case C
	it("fails a non-stable-semver tag", () => {
		for (const tag_name of ["v0.10.3-nightly.202606270300", "v0.10", "0.10.2", "latest"]) {
			const result = evaluateLatestRelease({ status: 200, body: { ...stable, tag_name } });
			expect(result.outcome, tag_name).toBe("fail");
			expect(result.message).toMatch(/expected a stable semver tag/i);
		}
	});

	it("fails when latest resolves to a draft or a prerelease", () => {
		expect(evaluateLatestRelease({ status: 200, body: { ...stable, draft: true } })).toMatchObject({
			outcome: "fail",
			message: expect.stringMatching(/draft\/prerelease/i),
		});
		expect(evaluateLatestRelease({ status: 200, body: { ...stable, prerelease: true } })).toMatchObject({
			outcome: "fail",
			message: expect.stringMatching(/draft\/prerelease/i),
		});
	});

	// Case D
	it("fails when an updater feed asset is missing", () => {
		for (const missing of REQUIRED_ASSETS) {
			const body = { ...stable, assets: assets.filter((a) => a.name !== missing) };
			const result = evaluateLatestRelease({ status: 200, body });
			expect(result.outcome, missing).toBe("fail");
			expect(result.message).toContain(missing);
		}
	});

	it("fails a release with no assets at all", () => {
		expect(evaluateLatestRelease({ status: 200, body: { ...stable, assets: [] } }).outcome).toBe("fail");
	});

	// Case E - only the 404 is forgiven. Auth, permission, rate limit and
	// server-side faults still have to go red.
	it("fails on any non-200, non-404 status", () => {
		for (const status of [401, 403, 429, 500, 502]) {
			const result = evaluateLatestRelease({ status, body: { message: "boom" } });
			expect(result.outcome, String(status)).toBe("fail");
			expect(result.message).toContain(String(status));
		}
	});
});
