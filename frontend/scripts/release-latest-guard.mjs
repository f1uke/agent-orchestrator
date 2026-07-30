// Guards /releases/latest so a non-stable release can never take over
// electron-updater's stable channel. Dependency-free ESM (mirrors
// nightly-version.mjs) so the release-latest-guard workflow runs it directly
// and vitest unit-tests the decision logic without touching live GitHub.
//
// The workflow pipes `gh api --include repos/<repo>/releases/latest` in on
// stdin. --include is what makes the outcome decidable: the HTTP status is
// data, so "no stable release exists" (404) is told apart from auth, rate
// limit and server faults by status code rather than by matching gh's
// localized stderr text. A transport failure prints no status line at all and
// therefore throws rather than being mistaken for a 404.

// Updater feed assets every stable release must publish, one per platform
// channel electron-updater polls.
export const REQUIRED_ASSETS = ["latest.yml", "latest-mac.yml", "latest-linux.yml"];

const STABLE_SEMVER = /^v[0-9]+\.[0-9]+\.[0-9]+$/;

// parseGhApiResponse splits `gh api --include` output into its status code and
// JSON body. gh writes the status line LF-terminated, then CRLF headers, then
// a blank CRLF line before the body.
export function parseGhApiResponse(raw) {
	const match = /^HTTP\/[\d.]+ (\d{3})\b/.exec(String(raw));
	if (!match) {
		throw new Error("release-latest-guard: no HTTP status line in gh output (request never reached GitHub?)");
	}
	const separator = String(raw).indexOf("\r\n\r\n");
	const body = separator === -1 ? "" : String(raw).slice(separator + 4);
	return { status: Number(match[1]), body: JSON.parse(body) };
}

// evaluateLatestRelease decides the guard outcome for one API response:
// "skip" (warn, exit 0), "pass" (exit 0) or "fail" (error, exit 1).
export function evaluateLatestRelease({ status, body }) {
	// /releases/latest ignores prereleases, so a repo publishing only nightlies
	// legitimately 404s. Nothing to validate yet - warn instead of going red.
	if (status === 404) {
		return {
			outcome: "skip",
			message: "No stable GitHub release exists yet; skipping latest-release asset validation.",
		};
	}
	if (status !== 200) {
		return { outcome: "fail", message: `GitHub API returned HTTP ${status} for the latest release.` };
	}

	const tag = body.tag_name;
	if (body.draft === true || body.prerelease === true) {
		return {
			outcome: "fail",
			message: `GitHub latest resolved to draft/prerelease '${tag}'; stable latest must be a published non-prerelease.`,
		};
	}
	if (!STABLE_SEMVER.test(String(tag))) {
		return {
			outcome: "fail",
			message: `GitHub latest resolved to '${tag}'; expected a stable semver tag like v0.10.2.`,
		};
	}

	const names = (body.assets ?? []).map((a) => a.name);
	for (const required of REQUIRED_ASSETS) {
		if (!names.includes(required)) {
			return {
				outcome: "fail",
				message: `Latest stable release '${tag}' is missing updater feed asset '${required}'.`,
			};
		}
	}

	return { outcome: "pass", message: `Latest stable release '${tag}' has every updater feed asset.` };
}

// CLI entry for CI: gh api --include ... | node scripts/release-latest-guard.mjs
// Emits a GitHub Actions annotation and exits 0 (skip/pass) or 1 (fail).
if (import.meta.url === `file://${process.argv[1]}`) {
	const raw = await new Promise((resolve, reject) => {
		let buf = "";
		process.stdin.setEncoding("utf8");
		process.stdin.on("data", (chunk) => (buf += chunk));
		process.stdin.on("end", () => resolve(buf));
		process.stdin.on("error", reject);
	});

	let result;
	try {
		result = evaluateLatestRelease(parseGhApiResponse(raw));
	} catch (err) {
		// Unreadable response (transport failure, non-JSON body). Annotate
		// rather than dumping a stack trace, but still fail the run.
		process.stdout.write(`::error::${err.message}\n`);
		process.exit(1);
	}

	const { outcome, message } = result;
	const prefix = outcome === "skip" ? "::warning::" : outcome === "fail" ? "::error::" : "";
	process.stdout.write(`${prefix}${message}\n`);
	process.exit(outcome === "fail" ? 1 : 0);
}
