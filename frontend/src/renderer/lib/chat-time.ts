/** One way to say how long something took or has run: `45s`, `2m 14s`, `1h 5m`. */
export function formatDuration(ms: number): string {
	const seconds = Math.max(0, Math.floor(ms / 1000));
	if (seconds < 60) return `${seconds}s`;
	const minutes = Math.floor(seconds / 60);
	if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
	return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

/** Milliseconds from one log timestamp to another, or undefined when either is missing or unreadable. */
export function elapsedMs(from: string | undefined, to: string | undefined): number | undefined {
	const start = Date.parse(from ?? "");
	const end = Date.parse(to ?? "");
	return Number.isNaN(start) || Number.isNaN(end) ? undefined : end - start;
}

/** A log timestamp as the local wall-clock time, `17:51`. */
export function clockTime(ts: string): string {
	const d = new Date(ts);
	if (Number.isNaN(d.getTime())) return "";
	return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}
