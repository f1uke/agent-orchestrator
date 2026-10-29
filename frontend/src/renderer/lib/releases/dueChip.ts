import { plural } from "./plural";
import type { AlertDue } from "./types";
import type { Tone } from "./vocab";

export type DueChip = { text: string; tone: Tone };

export function dueChip(d: AlertDue): DueChip {
	if (d.missed) return { text: "Missed", tone: "failed" };
	const n = d.workDays;
	if (n < 0) return { text: `${plural(-n, "day")} late`, tone: "failed" };
	if (n === 0) return { text: "Today", tone: "failed" };
	return { text: `${plural(n, "day")} left`, tone: n <= 1 ? "failed" : n <= 3 ? "waiting" : "unknown" };
}
