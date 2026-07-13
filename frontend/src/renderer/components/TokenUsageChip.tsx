import { Coins, Flame } from "lucide-react";
import { formatCompactTokens, tokenUsageTitle } from "../lib/format-tokens";
import { cn } from "../lib/utils";
import type { TokenUsage } from "../types/workspace";

/**
 * The board-card token/cost chip. Headlines the session's compact COST-WEIGHTED
 * total — the honest "real spend" number, since cache_read (which bills at ~0.1×
 * a normal input token yet dominates the raw total) would otherwise make the chip
 * read far scarier than the actual cost. The full breakdown — raw, cost-weighted,
 * turns, per-bucket split — is on hover.
 *
 * The runaway flame is still driven by the daemon's RAW-total threshold (the
 * calibrated, loop-sensitive signal); in practice a raw blow-up carries a large
 * cost-weighted total too, so the flame lands on a chip whose headline is already
 * high. A runaway session reddens the chip and swaps the coins for a flame so it
 * reads as "something is looping here".
 *
 * Renders nothing when the session has no telemetry (a non-claude-code agent, or a
 * session AO has not parsed yet), so those cards simply show no chip.
 */
export function TokenUsageChip({ usage }: { usage?: TokenUsage }) {
	if (!usage) return null;
	const Icon = usage.runaway ? Flame : Coins;
	return (
		<span
			aria-label={`Token cost ${formatCompactTokens(usage.costWeighted)}${usage.runaway ? ", runaway" : ""}`}
			className={cn(
				"inline-flex shrink-0 items-center gap-1 rounded-full border px-1.5 py-0.5 text-[10px] font-medium tabular-nums",
				usage.runaway
					? "border-[color-mix(in_srgb,var(--red)_55%,transparent)] text-error"
					: "border-[color-mix(in_srgb,var(--fg-passive)_30%,transparent)] text-passive",
			)}
			title={tokenUsageTitle(usage)}
		>
			<Icon className="h-3 w-3" strokeWidth={2} />
			{formatCompactTokens(usage.costWeighted)}
		</span>
	);
}
