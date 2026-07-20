// Pure helpers + palette for the Tests-tab "Smoke test" checklist. The tab is
// pixel-matched to a hand-authored design (Tests.dc.html), but each value below
// resolves to a CSS custom property (defined per theme in `styles.css`) rather
// than a raw hex — otherwise the tab stays dark when the app is in light mode.
// Surfaces, borders and the text ramp come from the --inbox-* tokens the sibling
// Comments tab uses (the two tabs share one design language); only the
// checklist-specific roles carry --smoke-* tokens. The dark values behind these
// tokens are still the verbatim handoff hexes; see the block in `styles.css`.

import type { components } from "../../api/schema";
import { ACCENT, MONO, accentMix, tint } from "./comment-inbox";

/** Re-exported so the Tests-tab components keep a single import site. The accent
 * is the app-wide token — this tab used to carry its own `#3b82f6` copy. */
export { ACCENT, MONO, accentMix };

export type SmokeCheck = components["schemas"]["SmokeCheck"];
export type SmokeEvidence = components["schemas"]["SmokeEvidence"];
export type SmokeVerdict = "pending" | "pass" | "fail" | "skip";

/** Design palette, by role. Each value is a themed token, not a literal. */
export const PALETTE = {
	rail: "var(--inbox-rail)",
	cardBg: "var(--smoke-card-bg)",
	cardBgOpen: "var(--inbox-card-bg)",
	pillBg: "var(--inbox-pill-bg)",
	whyBg: "var(--smoke-why-bg)",
	trackBg: "var(--smoke-track-bg)",
	reportBg: "var(--inbox-batch-bg)",
	// borders
	divider: "var(--inbox-divider)",
	borderCard: "var(--inbox-border-card)",
	borderCardOpen: "var(--smoke-card-border-open)",
	borderPill: "var(--inbox-border-pill)",
	borderExpand: "var(--smoke-divider-expand)",
	borderReport: "var(--inbox-border-batch)",
	// text
	textStrong: "var(--inbox-text-strong)",
	text: "var(--inbox-text)",
	body: "var(--inbox-body)",
	secondary: "var(--inbox-secondary)",
	secondary2: "var(--inbox-secondary-2)",
	muted: "var(--inbox-muted)",
	muted2: "var(--inbox-muted-2)",
	/** Monospace PR/file-ref chips — same ramp as the viewer's path chrome. */
	refChip: "var(--viewer-chrome-fg)",
	/** Load-error copy, shared with the Failed verdict hue. */
	danger: "var(--smoke-fail-fg)",
	/** "· by you · 2h ago" — sits on the verdict pill's tint, so not `muted`. */
	caption: "var(--smoke-caption)",
	/** Accent label on an accent-tinted fill (Post to Jira), so not `ACCENT`. */
	accentText: "var(--smoke-accent-text)",
	// progress segments
	segPass: "var(--smoke-pass)",
	segFail: "var(--smoke-fail)",
	segSkip: "var(--smoke-skip)",
	// expected box
	expectedBg: tint("var(--smoke-pass)", 6),
	expectedBorder: tint("var(--smoke-pass)", 28),
	expectedBody: "var(--smoke-expected-body)",
	evidenceOn: "var(--smoke-evidence-on)",
	// Pass/Fail decision buttons (softer than the verdict pills' fills).
	passBtnBg: tint("var(--smoke-pass)", 12),
	failBtnBg: tint("var(--smoke-fail)", 12),
} as const;

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
		color: "var(--smoke-pass-fg)",
		icon: "✓",
		pillBg: tint("var(--smoke-pass)", 14),
		pillBorder: tint("var(--smoke-pass)", 40),
	},
	fail: {
		label: "Failed",
		color: "var(--smoke-fail-fg)",
		icon: "✗",
		pillBg: tint("var(--smoke-fail)", 14),
		pillBorder: tint("var(--smoke-fail)", 45),
	},
	pending: {
		label: "To check",
		color: "var(--inbox-secondary)",
		icon: "○",
		pillBg: "var(--inbox-pill-bg)",
		pillBorder: "var(--inbox-border-menu)",
	},
	skip: {
		label: "Skipped",
		color: "var(--inbox-secondary)",
		icon: "⊘",
		pillBg: "var(--inbox-pill-bg)",
		pillBorder: "var(--inbox-border-menu)",
	},
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
