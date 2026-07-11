// Pure helpers + palette for the Tests-tab "Smoke test" checklist. The tab is
// pixel-matched to a hand-authored design (Tests.dc.html), so the exact hex
// values live here rather than in the shared token layer — mirroring the sibling
// Comments tab's lib/comment-inbox.ts precedent.

import type { components } from "../../api/schema";

export type SmokeCheck = components["schemas"]["SmokeCheck"];
export type SmokeEvidence = components["schemas"]["SmokeEvidence"];
export type SmokeVerdict = "pending" | "pass" | "fail" | "skip";

/** Accent used across the tab (report button, WHY box, avatar chip, selection). */
export const ACCENT = "#3b82f6";

/** Monospace stack for PR/file refs and step badges. */
export const MONO = 'ui-monospace, "SF Mono", Menlo, monospace';

/** Exact design palette, grouped by role — verbatim from the Tests.dc.html handoff. */
export const PALETTE = {
	rail: "#0a0a0c",
	header: "#0a0a0c",
	cardBg: "#0b0b0e",
	cardBgOpen: "#0d0d10",
	pillBg: "#18181c",
	whyBg: "#0e0e12",
	trackBg: "#161619",
	reportBg: "#101014",
	toastBg: "#1b1b20",
	// borders
	divider: "#141417",
	borderCard: "#1c1c20",
	borderCardOpen: "#26262c",
	borderPill: "#232327",
	borderExpand: "#17171a",
	borderReport: "#24242a",
	borderToast: "#33333a",
	// text
	textStrong: "#f2f2f5",
	text: "#e7e7ea",
	body: "#c2c2c8",
	secondary: "#9a9aa0",
	secondary2: "#8b8b92",
	muted: "#6c6c72",
	muted2: "#5c5c63",
	// progress segments
	segPass: "#4fae74",
	segFail: "#e0655e",
	segSkip: "#3a3a42",
	// expected box
	expectedBorder: "rgba(79,174,116,.28)",
	expectedBody: "#b7c9bd",
	evidenceOn: "#8b9a90",
} as const;

/** color-mix tint of the accent (oklab), used for soft fills/borders. */
export function accentMix(pct: number, base = "transparent"): string {
	return `color-mix(in oklab, ${ACCENT} ${pct}%, ${base})`;
}

export type VerdictMeta = {
	label: string;
	color: string;
	icon: string;
	pillBg: string;
	pillBorder: string;
};

/** Authoritative per-verdict colors/labels/icons (Tests.dc.html §2 table). */
export const VERDICT_META: Record<SmokeVerdict, VerdictMeta> = {
	pass: {
		label: "Passed",
		color: "#68c48c",
		icon: "✓",
		pillBg: "rgba(79,174,116,.14)",
		pillBorder: "rgba(79,174,116,.4)",
	},
	fail: {
		label: "Failed",
		color: "#e88f8f",
		icon: "✗",
		pillBg: "rgba(224,101,94,.14)",
		pillBorder: "rgba(224,101,94,.45)",
	},
	pending: { label: "To check", color: "#9a9aa0", icon: "○", pillBg: "#18181c", pillBorder: "#2a2a30" },
	skip: { label: "Skipped", color: "#9a9aa0", icon: "⊘", pillBg: "#18181c", pillBorder: "#2a2a30" },
};

export function verdictMeta(v: string): VerdictMeta {
	return VERDICT_META[(v as SmokeVerdict) in VERDICT_META ? (v as SmokeVerdict) : "pending"];
}

export type SmokeProgress = {
	total: number;
	pass: number;
	fail: number;
	skip: number;
	pending: number;
	checked: number;
};

/** Counts for the progress bar + counts row. */
export function progressFor(checks: SmokeCheck[]): SmokeProgress {
	const p: SmokeProgress = { total: checks.length, pass: 0, fail: 0, skip: 0, pending: 0, checked: 0 };
	for (const c of checks) {
		switch (c.verdict) {
			case "pass":
				p.pass += 1;
				break;
			case "fail":
				p.fail += 1;
				break;
			case "skip":
				p.skip += 1;
				break;
			default:
				p.pending += 1;
		}
	}
	p.checked = p.total - p.pending;
	return p;
}

/** Ordered progress-bar segments (pass, fail, skip); pending shows as the track. */
export function progressSegments(p: SmokeProgress): { color: string; count: number }[] {
	return [
		{ color: PALETTE.segPass, count: p.pass },
		{ color: PALETTE.segFail, count: p.fail },
		{ color: PALETTE.segSkip, count: p.skip },
	];
}

/** "CHECK N" tag derived from a case's 1-based seq. */
export function checkTag(seq: number): string {
	return `CHECK ${seq}`;
}

/**
 * Compact relative time ("just now", "5m ago", "2h ago", "3d ago") for the
 * "by you · <when>" verdict caption. NOTE: approximate, like the Comments tab.
 */
export function relativeTime(iso: string | null | undefined, now: number): string {
	if (!iso) return "";
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

/** Whether a MIME type is a video we accept as evidence. */
export function isVideoMime(mime: string): boolean {
	return mime.startsWith("video/");
}
