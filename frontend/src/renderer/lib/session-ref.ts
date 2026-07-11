// AO session-id reference helpers — the `@`session convention (see the AO
// id-reference doc). A session's canonical id is `<project>-<num>`; the
// human/agent-facing reference form is `@<project>-<num>` (full, preferred) or
// `@<num>` (short, project implied). This module centralizes the display form
// (sidebar) and the terminal linkifier's token matching so both stay in sync.

/** The full display reference for a session: `@<canonical-id>`. */
export function sessionRefLabel(sessionId: string): string {
	return `@${sessionId}`;
}

export type SessionLinkMatch = {
	/** 0-based char offset of the token's first char within the line. */
	startIndex: number;
	/** 0-based char offset one past the token's last char. */
	endIndex: number;
	/** The resolved canonical session id (`<project>-<num>`). */
	sessionId: string;
};

export type ResolveOptions = {
	/** Canonical ids of every known session, across all projects. */
	knownIds: ReadonlySet<string>;
	/** The terminal's own session's project id — expands the `@<num>` short form. */
	currentProjectId?: string;
};

// A candidate session reference in a line of terminal text. Three shapes:
//   @<project>-<num>   sigil + full canonical id
//   <project>-<num>    bare canonical id (as it appears in logs / `[from …]`)
//   @<num>             short form (project implied by the terminal's own session)
// The id body is lowercase [a-z0-9-] so an uppercase Jira key like STAR-2272 is
// never even a candidate; resolution against the known-id set is the real gate.
// A bare number without `@` is deliberately NOT matched (never linkify a raw
// number). Group 1 is a leading boundary (line start or a non-word/@/- char) so
// we never match inside a longer word; the trailing lookahead forbids a
// continuing word char so partial ids (e.g. `…-60-foo`) do not match.
const TOKEN_RE = /(^|[^\w@-])(@?[a-z0-9][a-z0-9-]*-\d+|@\d+)(?![\w-])/g;

// Resolve a matched token to a canonical session id, or null when it does not
// name a known session. `@<num>` expands within currentProjectId; every other
// form must match a known id exactly (case-sensitive — real ids are lowercase).
export function resolveSessionToken(token: string, opts: ResolveOptions): string | null {
	const raw = token.startsWith("@") ? token.slice(1) : token;
	if (/^\d+$/.test(raw)) {
		if (!opts.currentProjectId) return null;
		const candidate = `${opts.currentProjectId}-${raw}`;
		return opts.knownIds.has(candidate) ? candidate : null;
	}
	return opts.knownIds.has(raw) ? raw : null;
}

// Find every session reference in one line of terminal text that resolves to a
// known session, with its char range (consumed by the xterm link provider).
// Unknown ids and non-session tokens (a Jira key, a hyphenated word) are dropped
// by resolution. A fresh RegExp per call keeps lastIndex state local/reentrant.
export function findSessionLinks(line: string, opts: ResolveOptions): SessionLinkMatch[] {
	const matches: SessionLinkMatch[] = [];
	const re = new RegExp(TOKEN_RE.source, "g");
	let m: RegExpExecArray | null;
	while ((m = re.exec(line)) !== null) {
		const token = m[2];
		const start = m.index + m[1].length;
		const sessionId = resolveSessionToken(token, opts);
		if (sessionId) matches.push({ startIndex: start, endIndex: start + token.length, sessionId });
	}
	return matches;
}
