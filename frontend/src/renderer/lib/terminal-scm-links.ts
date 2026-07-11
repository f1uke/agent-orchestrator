// Terminal linkifier for SCM reference tokens — the `#`/`!` half of the AO
// id-reference convention (see the id-reference doc). `#<num>` names a GitHub
// pull request / issue, `!<num>` a GitLab merge request. Unlike the
// `@<project>-<num>` session refs in session-ref.ts (which navigate INTERNALLY on
// the board), these open the PR/MR in the OS browser.
//
// The owner/repo/host is NEVER hardcoded: it is derived from the session's own
// remote — the base of an observed PR/MR URL (`session.prs[].url`, from the
// daemon's SCM facts, which carries the full host including self-hosted GitLab) —
// exactly the provider split the Reviews/Comments UI already uses
// (`providerFromPRURL`/`prURL` in pr-display.ts). Matching is GATED by provider:
// `#` linkifies only when a GitHub remote is known and `!` only when a GitLab
// project is known; otherwise the token stays plain text (no dead/wrong link).
//
// Safety: the click URL is built by us from that trusted base plus a
// regex-validated integer (the token's `\d+`), so the host is always the
// session's own remote host by construction — the token text can only ever
// influence the numeric path segment, never the host. The URLs are https, which
// the #57 external-open path (window.open → main setWindowOpenHandler →
// shell.openExternal, and the scheme allowlist in open-terminal-link.ts) already
// permits, so no allowlist change is required.

/** The GitHub repo base + GitLab project base a terminal can resolve `#`/`!` against. */
export type ScmRemotes = {
	/** e.g. `https://github.com/owner/repo` — present iff a GitHub remote was observed. */
	githubRepoBase?: string;
	/** e.g. `https://gitlab.example.com/group/project` — present iff a GitLab project was observed. */
	gitlabProjectBase?: string;
};

export type ExternalRefMatch = {
	/** 0-based char offset of the token's first char (the `#`/`!` sigil) within the line. */
	startIndex: number;
	/** 0-based char offset one past the token's last digit. */
	endIndex: number;
	/** The absolute https URL the token resolves to (built from a trusted remote base). */
	url: string;
};

// GitLab merge requests live under an arbitrarily nested group/project path
// followed by this host-agnostic marker; everything before it is the project
// base. Mirrors pr-display.ts's `gitlabMRPathMarker`.
const GITLAB_MR_MARKER = "/-/merge_requests/";

/**
 * The GitHub repo base (`https://<host>/<owner>/<repo>`) for a GitHub PR/issue
 * URL, or undefined if `url` is not a GitHub PR/issue URL. Self-hosted GitHub
 * Enterprise hosts are handled — the host comes from the URL, never hardcoded.
 */
export function githubRepoBaseFromUrl(url: string): string | undefined {
	let parsed: URL;
	try {
		parsed = new URL(url);
	} catch {
		return undefined;
	}
	// A GitLab MR/issue path (contains `/-/`) is never a GitHub URL.
	if (parsed.pathname.includes("/-/")) return undefined;
	const match = parsed.pathname.match(/^\/([^/]+\/[^/]+)\/(?:pull|issues)\/\d+/);
	if (!match) return undefined;
	return `${parsed.origin}/${match[1]}`;
}

/**
 * The GitLab project base (`https://<host>/<group…>/<project>`) for a GitLab MR
 * URL, or undefined if `url` is not a GitLab MR URL. The host and the full
 * (possibly nested) group/project path come from the URL, never hardcoded.
 */
export function gitlabProjectBaseFromUrl(url: string): string | undefined {
	let parsed: URL;
	try {
		parsed = new URL(url);
	} catch {
		return undefined;
	}
	const idx = parsed.pathname.indexOf(GITLAB_MR_MARKER);
	if (idx <= 0) return undefined;
	return `${parsed.origin}${parsed.pathname.slice(0, idx)}`;
}

/**
 * Reduce a set of observed PR/MR URLs to the project's GitHub repo base and
 * GitLab project base (first of each wins). Absent bases gate off the
 * corresponding sigil. All sessions in a project share the same remote(s), so
 * callers pass every observed URL in the project.
 */
export function resolveScmRemotes(urls: Iterable<string>): ScmRemotes {
	const remotes: ScmRemotes = {};
	for (const url of urls) {
		if (!remotes.githubRepoBase) {
			const gh = githubRepoBaseFromUrl(url);
			if (gh) remotes.githubRepoBase = gh;
		}
		if (!remotes.gitlabProjectBase) {
			const gl = gitlabProjectBaseFromUrl(url);
			if (gl) remotes.gitlabProjectBase = gl;
		}
		if (remotes.githubRepoBase && remotes.gitlabProjectBase) break;
	}
	return remotes;
}

// A bare `#<digits>` / `!<digits>` token. The leading group is start-of-line or a
// char that is NOT a word char / `#` / `!` / `/` — so fragments (`a#1`, `##1`,
// `/#1` URL anchors) don't match — and the trailing lookahead forbids a
// continuing word char or `-`. Requiring a digit immediately after the sigil,
// plus that trailing boundary, means only an ALL-digit token bounded by
// whitespace/punctuation survives: every hex color that carries an a–f letter
// (`#3b82f6`, `#1a2b3c`) is rejected because its digits are followed by a letter,
// `#!`/`#-` never start with a digit, and `!=`/`!important`/`!!`/`[ ! -f ]`
// likewise fail the digit requirement. Group 2 is the numeric id.
const GITHUB_REF_RE = /(^|[^\w#!/])#(\d+)(?![\w-])/g;
const GITLAB_REF_RE = /(^|[^\w#!/])!(\d+)(?![\w-])/g;

function collectRefs(line: string, source: RegExp, toUrl: (num: string) => string, out: ExternalRefMatch[]): void {
	// Fresh RegExp per call keeps lastIndex state local/reentrant.
	const re = new RegExp(source.source, "g");
	let m: RegExpExecArray | null;
	while ((m = re.exec(line)) !== null) {
		const sigilStart = m.index + m[1].length; // the `#`/`!` char
		const num = m[2];
		out.push({ startIndex: sigilStart, endIndex: sigilStart + 1 + num.length, url: toUrl(num) });
	}
}

/**
 * Every GitHub `#<num>` / GitLab `!<num>` reference on one line that resolves
 * against a known remote, with its char range (consumed by the xterm link
 * provider). `#` is emitted only when `githubRepoBase` is known, `!` only when
 * `gitlabProjectBase` is known — so a project missing that remote leaves the
 * token as plain text. Matches are ordered by position.
 */
export function findExternalRefLinks(line: string, remotes: ScmRemotes): ExternalRefMatch[] {
	const matches: ExternalRefMatch[] = [];
	const { githubRepoBase, gitlabProjectBase } = remotes;
	if (githubRepoBase) {
		collectRefs(line, GITHUB_REF_RE, (num) => `${githubRepoBase}/pull/${num}`, matches);
	}
	if (gitlabProjectBase) {
		collectRefs(line, GITLAB_REF_RE, (num) => `${gitlabProjectBase}${GITLAB_MR_MARKER}${num}`, matches);
	}
	matches.sort((a, b) => a.startIndex - b.startIndex);
	return matches;
}
