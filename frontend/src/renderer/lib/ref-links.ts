/**
 * Work-item references in plain text - a Jira key, a GitLab `!N` - turned into
 * links, resolved against the user's own reference-link settings.
 *
 * The terminal already linkifies the same two token shapes (`terminal-scm-links`),
 * resolving them against the SESSION's remote. Text outside a session (a Wiki
 * Tasks row) has no remote to lean on, so it resolves against a configured one
 * instead; the Jira-key grammar and the URL shapes are shared, not restated.
 *
 * Three ways a merge request is written, all seen in real notes:
 *
 *  - `!1234`            - bare: the configured default repo.
 *  - `XYZ !187`         - a repo alias, then the reference. Also ``XYZ `!187` ``,
 *                         where only the reference sits in inline code.
 *  - `XYZ!187`, `group/project!187` - GitLab's own cross-project grammar: an
 *                         alias, or a full project path used as-is.
 *
 * 🗝 Never a guessed URL. Anything the settings do not answer stays plain text:
 * no Jira address, no GitLab address, no default repo, or an alias nobody
 * configured. An UPPERCASE word right before `!N` is read as an alias - that is
 * how aliases are written - so an unknown one leaves the reference unlinked
 * rather than quietly pointing it at the default repo, which would be a
 * confident link to the wrong merge request.
 *
 * Returns TOKENS, never markup: the caller builds elements, so text from a note
 * cannot reach the DOM as HTML. Every URL is a configured http(s) base (the
 * daemon validates it) plus characters these regexes allow.
 */
import { GITLAB_MR_MARKER, JIRA_BROWSE_MARKER, JIRA_KEY_RE } from "./terminal-scm-links";

export type RefLinkSettings = {
	jiraBaseUrl: string;
	gitlabBaseUrl: string;
	gitlabDefaultRepo: string;
	gitlabRepoAliases: Record<string, string>;
};

export type RefPart = { kind: "text"; value: string } | { kind: "ref"; value: string; url: string };

type Found = { start: number; end: number; url: string };

/** `!N`, not followed by more of a word - the same trailing edge the terminal uses. */
const MR_REF = /!(\d+)(?![\w-])/g;

/** A project path as written in text, and as the daemon stores one. */
const PROJECT_PATH = /^[\w.-]+(?:\/[\w.-]+)*$/;

/** What a written alias looks like: it starts with a letter. */
const ALIAS_WORD = /^[A-Za-z][\w.-]*$/;

/** An alias in its conventional spelling. Unknown ones must not fall back to the default repo. */
const ALIAS_SHAPED = /^[A-Z][A-Z0-9_-]*[A-Z0-9]$/;

/** Uppercase words that name the kind of thing, not a repo: "MR !12" is a bare reference. */
const NOT_AN_ALIAS = new Set(["MR", "PR"]);

const PATH_CHAR = /[\w./-]/;
const WORD_CHAR = /[\w.-]/;

/** Walks back from `end` over characters `allowed` accepts; returns where the run starts. */
function runStart(text: string, end: number, allowed: RegExp): number {
	let at = end;
	while (at > 0 && allowed.test(text[at - 1])) at--;
	return at;
}

function mrUrl(base: string, path: string, num: string): string {
	return `${base}/${path}${GITLAB_MR_MARKER}${num}`;
}

function lookupAlias(aliases: Record<string, string>, name: string): string | undefined {
	const wanted = name.toLowerCase();
	for (const [alias, path] of Object.entries(aliases)) {
		if (alias.toLowerCase() === wanted) return path;
	}
	return undefined;
}

/**
 * The repo a prefix written before `!N` names: a full project path as-is, a
 * configured alias, or nothing. `unknown` is a prefix that clearly names SOME
 * repo, just not one this user configured - the reference must stay plain.
 */
function repoForPrefix(prefix: string, aliases: Record<string, string>): string | "unknown" | undefined {
	if (prefix.includes("/")) {
		const valid = PROJECT_PATH.test(prefix) && prefix.split("/").every((segment) => segment.replace(/\./g, "") !== "");
		return valid ? prefix : "unknown";
	}
	if (!ALIAS_WORD.test(prefix)) return undefined;
	return lookupAlias(aliases, prefix);
}

function findMergeRequests(text: string, settings: RefLinkSettings, out: Found[]): void {
	const base = settings.gitlabBaseUrl;
	if (!base) return;
	for (const match of text.matchAll(MR_REF)) {
		const bang = match.index ?? 0;
		const end = bang + match[0].length;
		const num = match[1];
		const before = bang > 0 ? text[bang - 1] : "";

		// `prefix!N`: GitLab's own cross-project form, glued to the reference.
		// Glued to anything else it is not a reference at all.
		if (before !== "" && WORD_CHAR.test(before)) {
			const start = runStart(text, bang, PATH_CHAR);
			const path = repoForPrefix(text.slice(start, bang), settings.gitlabRepoAliases);
			if (path && path !== "unknown") out.push({ start, end, url: mrUrl(base, path, num) });
			continue;
		}
		// `#!1`, `!!1`, `/!1` are not references (the terminal's rule too).
		if (before === "#" || before === "!" || before === "/") continue;

		// `word !N` or ``word `!N` ``: the word may name the repo.
		let gap = bang;
		const quoted = text[gap - 1] === "`";
		if (quoted) gap--;
		const wordEnd = runStart(text, gap, /[ \t]/);
		if (wordEnd < gap) {
			const wordStart = runStart(text, wordEnd, PATH_CHAR);
			const word = text.slice(wordStart, wordEnd);
			const path = word === "" ? undefined : repoForPrefix(word, settings.gitlabRepoAliases);
			if (path === "unknown") continue;
			if (path) {
				// The repo belongs to the link when nothing but spaces separates
				// them; across a backtick only `!N` is linked, so the link never
				// spans the edge of inline code.
				out.push({ start: quoted ? bang : wordStart, end, url: mrUrl(base, path, num) });
				continue;
			}
			if (ALIAS_SHAPED.test(word) && !NOT_AN_ALIAS.has(word)) continue;
		}

		if (settings.gitlabDefaultRepo) {
			out.push({ start: bang, end, url: mrUrl(base, settings.gitlabDefaultRepo, num) });
		}
	}
}

function findJiraKeys(text: string, settings: RefLinkSettings, out: Found[]): void {
	const base = settings.jiraBaseUrl;
	if (!base) return;
	for (const match of text.matchAll(new RegExp(JIRA_KEY_RE.source, "g"))) {
		const start = (match.index ?? 0) + match[1].length;
		const key = match[2];
		out.push({ start, end: start + key.length, url: `${base}${JIRA_BROWSE_MARKER}${key}` });
	}
}

/**
 * Splits `text` into plain runs and references. With no settings, or none that
 * apply, it is one plain run - exactly the text it was given.
 */
export function splitRefLinks(text: string, settings: RefLinkSettings | undefined): RefPart[] {
	if (!settings || text === "") return [{ kind: "text", value: text }];
	const found: Found[] = [];
	findMergeRequests(text, settings, found);
	findJiraKeys(text, settings, found);
	// A merge request is found first and wins an overlap (`WEB-2 !5` with `WEB-2`
	// as an alias is one link, not a key beside a reference).
	const kept: Found[] = [];
	for (const candidate of found) {
		if (!kept.some((k) => candidate.start < k.end && k.start < candidate.end)) kept.push(candidate);
	}
	kept.sort((a, b) => a.start - b.start);

	const parts: RefPart[] = [];
	let cursor = 0;
	for (const link of kept) {
		if (link.start > cursor) parts.push({ kind: "text", value: text.slice(cursor, link.start) });
		parts.push({ kind: "ref", value: text.slice(link.start, link.end), url: link.url });
		cursor = link.end;
	}
	if (cursor < text.length) parts.push({ kind: "text", value: text.slice(cursor) });
	return parts.length > 0 ? parts : [{ kind: "text", value: text }];
}

/**
 * The alias map as the Settings page edits it: one `alias = group/project` per
 * line, sorted so the same map always reads the same way.
 */
export function formatAliasLines(aliases: Record<string, string>): string {
	return Object.keys(aliases)
		.sort((a, b) => a.localeCompare(b))
		.map((alias) => `${alias} = ${aliases[alias]}`)
		.join("\n");
}

/**
 * Reads the Settings page's alias lines back into a map. Blank lines are
 * skipped; a line without `=` throws, naming the line, so a typo is refused
 * rather than silently dropped. The daemon validates the names and paths.
 */
export function parseAliasLines(text: string): Record<string, string> {
	const aliases: Record<string, string> = {};
	text.split("\n").forEach((raw, index) => {
		const line = raw.trim();
		if (line === "") return;
		const eq = line.indexOf("=");
		const alias = eq < 0 ? "" : line.slice(0, eq).trim();
		const path = eq < 0 ? "" : line.slice(eq + 1).trim();
		if (alias === "" || path === "") {
			throw new Error(`Repo aliases, line ${index + 1}: write it as "alias = group/project".`);
		}
		aliases[alias] = path;
	});
	return aliases;
}
