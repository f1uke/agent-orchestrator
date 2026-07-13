import type { TokenUsage } from "../types/workspace";

/**
 * Format a token count compactly for a board chip: 1_234 → "1.2K",
 * 23_506_652 → "23.5M", 156_346_801 → "156M", 1_200_000_000 → "1.2B". Values ≥100
 * of a unit drop the decimal so the chip stays short; smaller values keep one.
 */
export function formatCompactTokens(n: number): string {
	if (!Number.isFinite(n) || n <= 0) return "0";
	const units: readonly [number, string][] = [
		[1e9, "B"],
		[1e6, "M"],
		[1e3, "K"],
	];
	for (const [div, suffix] of units) {
		if (n >= div) {
			const v = n / div;
			const label = v >= 100 ? Math.round(v).toString() : v.toFixed(1).replace(/\.0$/, "");
			return label + suffix;
		}
	}
	return Math.round(n).toString();
}

/** Multi-line hover title spelling out the full breakdown behind the chip. */
export function tokenUsageTitle(u: TokenUsage): string {
	const f = (x: number) => x.toLocaleString("en-US");
	return [
		`${f(u.rawTotal)} tokens raw · ${f(u.costWeighted)} cost-weighted · ${f(u.turns)} turns`,
		`input ${f(u.input)} · cache-write ${f(u.cacheCreation)} · cache-read ${f(u.cacheRead)} · output ${f(u.output)}`,
		u.runaway ? "⚠ runaway — well above a typical session; check for a stuck or looping agent" : "",
	]
		.filter(Boolean)
		.join("\n");
}
