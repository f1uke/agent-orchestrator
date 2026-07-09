// Pure helpers + palette for the Comments-tab "Unresolved inbox" redesign.
// The tab is pixel-matched to a hand-authored design (Comments Inbox.dc.html),
// so the exact hex values live here rather than in the shared token layer.

/** Accent used across the inbox (primary buttons, links, selection, worker actions). */
export const ACCENT = "#3b82f6";

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
	diffAddText: "#84cfa0",
	diffAddBg: "rgba(63,157,107,.12)",
	diffDelText: "#e69696",
	diffDelBg: "rgba(220,90,90,.12)",
	diffContextText: "#9a9aa0",
} as const;

/** color-mix tint of the accent (oklab), used for soft fills/borders. */
export function accentMix(pct: number, base = "transparent"): string {
	return `color-mix(in oklab, ${ACCENT} ${pct}%, ${base})`;
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

/**
 * The auto-generated worker prompt for a review comment — verbatim from the
 * design's `genPrompt`. Seeded (editable) into the "Edit prompt…" drawer and
 * used for batch "one task, all comments".
 */
export function genPrompt(path: string, line: number, body: string): string {
	return `A reviewer left this unresolved comment on ${path}:${line}\n\n> ${body}\n\nPlease address it: make the change, keep it minimal and consistent with the surrounding code, then reply on the thread summarizing what you did.`;
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
	return parts.length ? parts[parts.length - 1] : path ?? "";
}
