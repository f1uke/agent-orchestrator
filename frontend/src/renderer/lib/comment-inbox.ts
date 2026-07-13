// Pure helpers + palette for the Comments-tab "Unresolved inbox" redesign.
// The tab is pixel-matched to a hand-authored design (Comments Inbox.dc.html),
// so the exact hex values live here rather than in the shared token layer.

/** Accent used across the inbox (primary buttons, links, selection, worker actions). */
export const ACCENT = "#3b82f6";

/** Monospace stack for paths, line refs, code, and diffs (uses the app's bundled fonts). */
export const MONO = 'ui-monospace, "SF Mono", Menlo, monospace';

/** Exact design palette. Grouped by role; values are verbatim from the design handoff. */
export const PALETTE = {
	rail: "#0a0a0c",
	cardBg: "#0d0d10",
	fileHeader: "#111114",
	pillBg: "#18181c",
	menuBg: "#141418",
	replyBg: "#0f0f12",
	promptTextareaBg: "#0b0b0e",
	batchBg: "#101014",
	toastBg: "#1b1b20",
	resolvedBg: "#0b0b0e",
	// borders
	borderRail: "#17171a",
	borderCard: "#1c1c20",
	borderPill: "#232327",
	borderMenu: "#2a2a30",
	divider: "#141417",
	dividerCard: "#151518",
	borderBatch: "#24242a",
	borderToast: "#33333a",
	// text
	textStrong: "#f2f2f5",
	text: "#e7e7ea",
	body: "#c2c2c8",
	secondary: "#9a9aa0",
	secondary2: "#8b8b92",
	muted: "#6c6c72",
	muted2: "#5c5c63",
	muted3: "#54545a",
	// semantic
	green: "#5fb87a",
	amber: "#e0a544",
	red: "#ef6a63",
	code: "#e0a86a",
} as const;

/** color-mix tint of the accent (oklab), used for soft fills/borders. */
export function accentMix(pct: number, base = "transparent"): string {
	return `color-mix(in oklab, ${ACCENT} ${pct}%, ${base})`;
}

/**
 * Diff-row chrome shared by the inline (rail) diff and the full-file viewer:
 * added/removed lines get a background tint and a colored sign glyph, while the
 * code text itself is syntax-highlighted (see `tokenizeCode`). Verbatim from
 * the design's `styleDiffRow`.
 */
export const DIFF_ROW = {
	addBg: "rgba(63,157,107,.13)",
	delBg: "rgba(220,90,90,.13)",
	addSign: "#7fd8a0",
	delSign: "#e88f8f",
	contextSign: "#5c5c63",
} as const;

/** Token colors for the lightweight code highlighter — verbatim from the design's `C`. */
export const TOKEN_COLORS = {
	keyword: "#FC5FA3",
	string: "#FC6A5D",
	comment: "#6C7986",
	number: "#D0BF69",
	type: "#5DD8FF",
	fn: "#67B7A4",
	plain: "#E8E8EA",
} as const;

// Go/Swift/TS keyword set the highlighter recognizes — verbatim from the design.
const CODE_KEYWORDS = new Set([
	"func",
	"return",
	"if",
	"else",
	"switch",
	"case",
	"default",
	"struct",
	"let",
	"var",
	"some",
	"for",
	"range",
	"nil",
	"true",
	"false",
	"bool",
	"string",
	"int",
	"int64",
	"uint",
	"byte",
	"error",
	"defer",
	"package",
	"import",
	"type",
	"map",
	"interface",
	"chan",
	"go",
	"const",
	"self",
	"guard",
	"while",
	"in",
	"enum",
	"protocol",
	"extension",
	"class",
	"static",
	"private",
	"public",
	"override",
	"throws",
	"try",
	"async",
	"await",
]);

export type CodeToken = { text: string; color: string };

/**
 * Split one line of source into colored runs (keywords / strings / comments /
 * numbers / capitalized types / calls / plain). Language-agnostic and
 * deliberately simple — ported verbatim from the design's `tokenize`. Lossless:
 * concatenating the token texts reproduces the input line.
 */
export function tokenizeCode(line: string): CodeToken[] {
	if (!line) return [];
	const out: CodeToken[] = [];
	const push = (text: string, color: string) => {
		if (text) out.push({ text, color });
	};
	const re = /(\/\/.*$)|("(?:[^"\\]|\\.)*")|(\b\d+(?:\.\d+)?\b)|([A-Za-z_][A-Za-z0-9_]*)/g;
	let last = 0;
	let m: RegExpExecArray | null;
	while ((m = re.exec(line)) !== null) {
		if (m.index > last) push(line.slice(last, m.index), TOKEN_COLORS.plain);
		if (m[1]) push(m[1], TOKEN_COLORS.comment);
		else if (m[2]) push(m[2], TOKEN_COLORS.string);
		else if (m[3]) push(m[3], TOKEN_COLORS.number);
		else {
			const w = m[4];
			const isCall = /^\s*\(/.test(line.slice(re.lastIndex));
			if (CODE_KEYWORDS.has(w)) push(w, TOKEN_COLORS.keyword);
			else if (isCall) push(w, TOKEN_COLORS.fn);
			else if (/^[A-Z]/.test(w)) push(w, TOKEN_COLORS.type);
			else push(w, TOKEN_COLORS.plain);
		}
		last = re.lastIndex;
	}
	if (last < line.length) push(line.slice(last), TOKEN_COLORS.plain);
	return out;
}

export type BodyRun = { text: string; code: boolean };

/**
 * Split a comment body into plain / inline-`code` runs (backtick-delimited),
 * matching the design's `splitRuns`. Odd segments are code; empty runs dropped.
 */
export function splitBodyRuns(body: string): BodyRun[] {
	return (body ?? "")
		.split("`")
		.map((text, i) => ({ text, code: i % 2 === 1 }))
		.filter((r) => r.text !== "");
}

export type NoteRun = { text: string; href?: string };

// Matches a single markdown link: [label](target). Kept deliberately simple —
// GitLab system notes embed exactly one such link and no nested brackets.
const MD_LINK = /\[([^\]]+)\]\(([^)\s]+)\)/g;

/**
 * Split a GitLab system-note body (e.g. "changed this line in
 * [version 6 of the diff](/g/p/-/merge_requests/7/diffs?diff_id=1)") into plain
 * and link runs, so the UI can render the markdown link as a real hyperlink
 * instead of dumping the raw URL. `origin` (the PR/MR host) resolves GitLab's
 * host-relative link targets to absolute URLs. Runs with an `href` are links;
 * concatenating every run's `text` reproduces the visible label text.
 */
export function splitNoteRuns(body: string, origin = ""): NoteRun[] {
	const s = body ?? "";
	const out: NoteRun[] = [];
	let last = 0;
	let m: RegExpExecArray | null;
	MD_LINK.lastIndex = 0;
	while ((m = MD_LINK.exec(s)) !== null) {
		if (m.index > last) out.push({ text: s.slice(last, m.index) });
		out.push({ text: m[1], href: resolveNoteHref(m[2], origin) });
		last = MD_LINK.lastIndex;
	}
	if (last < s.length) out.push({ text: s.slice(last) });
	return out.filter((r) => r.text !== "");
}

/**
 * Resolve a note link target against the PR's origin. Absolute http(s) URLs pass
 * through; GitLab's host-relative targets ("/group/repo/-/…") are prefixed with
 * `origin` so Electron's window-open handler (http(s)-only) opens them.
 */
export function resolveNoteHref(href: string, origin: string): string {
	const h = (href ?? "").trim();
	if (/^https?:\/\//i.test(h)) return h;
	if (h.startsWith("/") && origin) return origin.replace(/\/+$/, "") + h;
	return h;
}

/** scheme://host of a PR/MR URL, used to resolve host-relative note links. */
export function originOf(url: string): string {
	try {
		return new URL(url).origin;
	} catch {
		return "";
	}
}

/**
 * The auto-generated worker prompt for a review comment — verbatim from the
 * design's `genPrompt`. Seeded (editable) into the "Edit prompt…" drawer and
 * used for batch "one task, all comments".
 */
export function genPrompt(path: string, line: number, body: string): string {
	return `A reviewer left this unresolved comment on ${path}:${line}\n\n> ${body}\n\nPlease address it: make the change, keep it minimal and consistent with the surrounding code, then reply on the thread summarizing what you did.`;
}

/** One unresolved review comment fed into {@link batchPrompt}. */
export interface BatchPromptItem {
	path: string;
	line: number;
	body: string;
}

/**
 * The worker prompt for "one task, all comments". A single comment reuses the
 * natural singular phrasing of {@link genPrompt}; multiple comments state the
 * shared instruction ONCE, then list each comment compactly by `path:line` with
 * its quoted body so the worker can locate and reply on the right thread - no
 * per-comment boilerplate repetition.
 */
export function batchPrompt(items: BatchPromptItem[]): string {
	if (items.length === 1) {
		const it = items[0];
		return genPrompt(it.path, it.line, it.body);
	}
	const lead = `There are ${items.length} unresolved review comments to address. For each: make the change, keep it minimal and consistent with the surrounding code, then reply on that thread summarizing what you did.`;
	const list = items.map((it, i) => `${i + 1}. ${it.path}:${it.line}\n   > ${it.body}`).join("\n");
	return `${lead}\n\n${list}`;
}

/** Avatar initials: first letters of the first two word-parts, else first two chars. */
export function initialsFor(name: string): string {
	const n = (name ?? "").trim();
	if (!n) return "?";
	const parts = n.split(/[^a-zA-Z0-9]+/).filter(Boolean);
	if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
	return n.slice(0, 2).toUpperCase();
}

/** Deterministic hue (0–359) from a name, for the oklch avatar background. */
export function hueFor(name: string): number {
	let h = 0;
	const n = name ?? "";
	for (let i = 0; i < n.length; i++) h = (h * 31 + n.charCodeAt(i)) % 360;
	return h;
}

export function avatarBg(name: string, lightness = 0.55, chroma = 0.13): string {
	return `oklch(${lightness} ${chroma} ${hueFor(name)})`;
}

export type StatusKind = "changes" | "open" | "conflict";

/** Derive the PR status pill from the session's PR facts (matches the design's three states). */
export function statusFor(review?: string, mergeability?: string): { label: string; kind: StatusKind } {
	if (mergeability === "conflicting") return { label: "Conflict", kind: "conflict" };
	if (review === "changes_requested") return { label: "Changes requested", kind: "changes" };
	return { label: "Open", kind: "open" };
}

export const STATUS_COLORS: Record<StatusKind, { color: string; bg: string; border: string }> = {
	changes: { color: "#e0a544", bg: "rgba(224,165,68,.12)", border: "rgba(224,165,68,.25)" },
	open: { color: "#5fb87a", bg: "rgba(95,184,122,.12)", border: "rgba(95,184,122,.28)" },
	conflict: { color: "#ef6a63", bg: "rgba(239,106,99,.12)", border: "rgba(239,106,99,.28)" },
};

/** Short provider badge: GitLab → "GL", everything else → "GH". */
export function providerBadge(provider: string): string {
	return (provider ?? "").toLowerCase() === "gitlab" ? "GL" : "GH";
}

/**
 * Compact relative time ("just now", "5m ago", "2h ago", "1d ago"). NOTE: the
 * stored createdAt is observe-time (when the daemon fetched the comment), not
 * authoring-time, so this is approximate — matching the design's timestamps.
 */
export function relativeTime(iso: string, now: number): string {
	const t = Date.parse(iso);
	if (Number.isNaN(t)) return "";
	const s = Math.max(0, Math.floor((now - t) / 1000));
	if (s < 60) return "just now";
	const m = Math.floor(s / 60);
	if (m < 60) return `${m}m ago`;
	const h = Math.floor(m / 60);
	if (h < 24) return `${h}h ago`;
	const d = Math.floor(h / 24);
	if (d < 30) return `${d}d ago`;
	const mo = Math.floor(d / 30);
	if (mo < 12) return `${mo}mo ago`;
	return `${Math.floor(mo / 12)}y ago`;
}

/** Last path segment (filename) — used in "Sent to worker · {file}" toasts. */
export function baseName(path: string): string {
	const parts = (path ?? "").split("/").filter(Boolean);
	return parts.length ? parts[parts.length - 1] : (path ?? "");
}
