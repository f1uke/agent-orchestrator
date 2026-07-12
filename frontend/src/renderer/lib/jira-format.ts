import type { CSSProperties } from "react";

// Shared presentational helpers for Jira issue rendering — used by both the Summary
// tab (JiraIssueSection) and the Browse Jira detail view (JiraIssueDetail) so the
// two render an issue identically. Pure, so the semantics stay unit-testable.

// statusPillStyle tints a status pill from Jira's status CATEGORY (not the free-form
// name): new → amber (to-do / needs attention), indeterminate → blue (in progress),
// done → green. Mirrors the TimelinePill color-mix treatment.
export function statusPillStyle(category?: string): CSSProperties {
	const tone = statusTone(category);
	return {
		color: tone,
		background: `color-mix(in srgb, ${tone} 14%, transparent)`,
		borderColor: `color-mix(in srgb, ${tone} 42%, transparent)`,
	};
}

export function statusTone(category?: string): string {
	switch (category) {
		case "done":
			return "var(--success)";
		case "indeterminate":
			return "var(--accent)";
		default:
			return "var(--amber)";
	}
}

export function priorityTone(priority: string): string {
	const p = priority.toLowerCase();
	if (p.includes("highest") || p.includes("high") || p.includes("critical") || p.includes("blocker")) return "var(--red)";
	if (p.includes("low")) return "var(--fg-muted)";
	return "var(--orange)";
}

// formatSprintDates renders "29 Jun – 10 Jul" from ISO start/end, dropping the pair
// when either date is missing/unparseable.
export function formatSprintDates(start?: string, end?: string): string {
	const s = parseDate(start);
	const e = parseDate(end);
	if (!s || !e) return "";
	const fmt = (d: Date) => d.toLocaleDateString(undefined, { day: "numeric", month: "short" });
	return `${fmt(s)} – ${fmt(e)}`;
}

function parseDate(iso?: string): Date | null {
	if (!iso) return null;
	const d = new Date(iso);
	return Number.isNaN(d.getTime()) ? null : d;
}
