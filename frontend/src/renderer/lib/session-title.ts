/**
 * Resolve the human-facing NAME for a session row/card.
 *
 * A Jira-linked session carries its key in two independent derivations:
 *   - the branch/worktree name keeps the key (`feature/STAR-2272-…`) — untouched here;
 *   - the DISPLAY name is the issue summary only, never the card number.
 *
 * The key already shows as its own badge (sidebar line 2 / board card), so it must
 * not also prefix the name. Every UI creation path already stores a key-free
 * summary as `displayName`, but this normalizer is the last line of defence for
 * names that arrive with a leading key anyway — legacy sessions, `ao spawn
 * --name "STAR-2272 …"`, or the raw `jira:<KEY>` binding surfacing when a session
 * has no display name. (Enhancement #5.)
 */

// A leading Jira-key token — `STAR-2272`, `[STAR-2272]`, `#STAR-2272`, or a
// `jira:STAR-2272` binding — followed by a separator (punctuation or whitespace)
// and MORE text. Anchored at the start and requiring trailing text so it only
// strips a key that PREFIXES a real summary, never a bare-key name (which has no
// summary to fall back to) or a key that merely appears mid-title.
const LEADING_JIRA_KEY_PREFIX = /^(?:jira:)?\[?#?\s*[A-Z][A-Z0-9]+-\d+\]?\s*(?:[-–—:.·|/]\s*|\s+)(?=\S)/;

/** Strip a leading Jira-key prefix (+ its separator) from a would-be display name.
 *  Returns the trimmed remainder, or the original (trimmed) string when there is
 *  no key prefix. A name that is nothing but a key is left intact. */
export function stripLeadingJiraKeyPrefix(name: string | null | undefined): string {
	if (!name) return "";
	return name.replace(LEADING_JIRA_KEY_PREFIX, "").trim();
}

type SessionNameParts = {
	displayName?: string | null;
	issueId?: string | null;
	id: string;
};

/** The name to show for a session: the human summary with any accidental leading
 *  Jira key stripped. Falls back to the issue id (a `jira:<KEY>` binding is shown
 *  as the bare key — the badge/branch carry it, and there is no summary to prefix)
 *  and finally the session id. */
export function displaySessionName(session: SessionNameParts): string {
	const named = stripLeadingJiraKeyPrefix(session.displayName);
	if (named) return named;

	const issueId = session.issueId?.trim();
	if (issueId) {
		const bound = /^jira:(.+)$/i.exec(issueId);
		if (bound) return bound[1].trim();
		const stripped = stripLeadingJiraKeyPrefix(issueId);
		if (stripped) return stripped;
		return issueId;
	}

	return session.id;
}
