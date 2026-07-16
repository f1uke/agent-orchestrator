// Pure helpers driving the daemon-loop countdown rings. Kept side-effect free so
// they are unit-testable and can be called from a render loop without surprises.
// All arguments are epoch milliseconds.

/**
 * Roll a loop's next-fire time forward by whole intervals until it is in the
 * future relative to now. The endpoint reports the next fire derived from the
 * last observed tick; between refetches the client advances it locally so the
 * ring keeps counting down through cycle boundaries instead of sticking at zero.
 */
export function effectiveNextRunAt(nextRunAtMs: number, intervalMs: number, nowMs: number): number {
	if (intervalMs <= 0) return nextRunAtMs;
	if (nextRunAtMs > nowMs) return nextRunAtMs;
	const missed = Math.floor((nowMs - nextRunAtMs) / intervalMs) + 1;
	return nextRunAtMs + missed * intervalMs;
}

/**
 * Fraction of the current cycle elapsed: 0 at the cycle start, approaching 1 as
 * the next fire nears. Clamped to [0, 1]. The cycle start is the effective next
 * fire minus one interval.
 */
export function computeFraction(nextRunAtMs: number, intervalMs: number, nowMs: number): number {
	if (intervalMs <= 0) return 0;
	const next = effectiveNextRunAt(nextRunAtMs, intervalMs, nowMs);
	const cycleStart = next - intervalMs;
	const elapsed = nowMs - cycleStart;
	return Math.min(1, Math.max(0, elapsed / intervalMs));
}

/**
 * Human "time until next run": "<1s" when due, "m:ss" under an hour, "Xh Ym"
 * beyond. remainingMs is clamped at zero.
 */
export function formatNextIn(remainingMs: number): string {
	const totalSeconds = Math.max(0, Math.floor(remainingMs / 1000));
	if (totalSeconds < 1) return "<1s";
	if (totalSeconds >= 3600) {
		const hours = Math.floor(totalSeconds / 3600);
		const minutes = Math.floor((totalSeconds % 3600) / 60);
		return `${hours}h ${minutes}m`;
	}
	const minutes = Math.floor(totalSeconds / 60);
	const seconds = totalSeconds % 60;
	return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

/**
 * Human interval label for a loop cadence, e.g. "every 30s", "every 5m",
 * "every 6h". intervalMs of 0 (disabled) returns "paused".
 */
export function formatInterval(intervalMs: number): string {
	if (intervalMs <= 0) return "paused";
	const seconds = Math.round(intervalMs / 1000);
	if (seconds % 3600 === 0) return `every ${seconds / 3600}h`;
	if (seconds % 60 === 0) return `every ${seconds / 60}m`;
	return `every ${seconds}s`;
}
