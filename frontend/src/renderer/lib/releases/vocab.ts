import type { Severity, StageKey, StageStatus } from "./types";

/** The `data-st` value a status paints with; releases.css maps each to `--c`. */
export type Tone =
	"done" | "running" | "waiting" | "failed" | "late" | "todo" | "unknown" | "hotfix" | "accent" | "passive";

export const STAGE_META: Readonly<Record<StageKey, { label: string; short: string; letter: string }>> = {
	freeze: { label: "Freeze + release MR", short: "Freeze", letter: "" },
	scan: { label: "Release MR pipeline (scan)", short: "Scan", letter: "S" },
	approve: { label: "Jira release approval", short: "Approval", letter: "A" },
	tag: { label: "Merged + tagged on main", short: "Merged", letter: "M" },
	publish: { label: "Publish", short: "Publish", letter: "●" },
	force: { label: "Force update", short: "Force update", letter: "F" },
};

export const STATUS_TEXT: Readonly<Record<StageStatus, string>> = {
	done: "done",
	running: "running",
	waiting: "waiting on people",
	failed: "failed",
	late: "late",
	todo: "not yet",
	unknown: "unknown",
	not_observable: "not connected",
};

export const TONE_COLOR: Readonly<Record<Tone, string>> = {
	done: "var(--green)",
	running: "var(--orange)",
	waiting: "var(--amber)",
	failed: "var(--red)",
	late: "var(--red)",
	todo: "var(--fg-passive)",
	unknown: "var(--fg-muted)",
	hotfix: "var(--purple)",
	accent: "var(--accent)",
	passive: "var(--fg-passive)",
};

export function stageTone(s: StageStatus): Tone {
	return s === "not_observable" ? "passive" : s;
}

export const SEVERITY_META: Readonly<Record<Severity, { label: string; tone: Tone; rank: number }>> = {
	block: { label: "Block", tone: "failed", rank: 0 },
	data: { label: "Data", tone: "failed", rank: 1 },
	risk: { label: "Risk", tone: "waiting", rank: 2 },
	hygiene: { label: "Cleanup", tone: "todo", rank: 3 },
	info: { label: "Info", tone: "running", rank: 4 },
};

export const SEVERITY_ORDER: readonly Severity[] = ["block", "data", "risk", "hygiene", "info"];

export const RULE_GROUPS: Readonly<Record<string, string>> = {
	A: "Data integrity",
	B: "Schedule",
	C: "Build and quality",
	D: "Approval and process",
	E: "After publish",
	F: "Cross-train",
	G: "Sprint cards and long-lived branches",
};
