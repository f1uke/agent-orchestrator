import { Clock, Moon } from "lucide-react";
import { cn } from "../lib/utils";
import { isMergeSuspended, type IdleCountdownLevel, type WorkspaceSession } from "../types/workspace";
import { useIdleCountdown } from "../hooks/useIdleCountdown";

// Escalating text colour as the deadline nears: muted (≤1d) → amber (≤6h) → red
// (≤1h). Maps to the DESIGN status palette (passive / warning / error).
const LEVEL_TEXT: Record<IdleCountdownLevel, string> = {
	soon: "text-passive",
	urgent: "text-warning",
	imminent: "text-error",
};

const LEVEL_BORDER: Record<IdleCountdownLevel, string> = {
	soon: "border-[color-mix(in_srgb,var(--fg-passive)_30%,transparent)]",
	urgent: "border-[color-mix(in_srgb,var(--amber)_45%,transparent)]",
	imminent: "border-[color-mix(in_srgb,var(--red)_55%,transparent)]",
};

/**
 * The board-card / sidebar-row idle affordance. Two mutually exclusive states,
 * both orthogonal to the session's lane (the card never leaves its column):
 *
 *  - Suspended: a moon chip. Its copy follows the REASON, because the two kinds
 *    of sleep answer "what happens if I open this" differently — the idle sweep
 *    freed its tmux and opening resumes it in place, but a crew member waiting
 *    for the baton stays asleep however long you look at it, and only an explicit
 *    Wake takes the turn. Saying "open to resume" about the second would promise
 *    something the daemon refuses.
 *  - Approaching suspension: an escalating countdown chip, surfaced ONLY within
 *    ~1d of the deadline (amber ≤6h, red + pulse ≤1h) so a session far from
 *    expiry stays quiet.
 *
 * Renders nothing when neither applies. `compact` shrinks it for the sidebar row
 * (glyph-only paused, bare time for the countdown) to keep rows near name-only.
 */
export function IdleStatusChip({ session, compact = false }: { session: WorkspaceSession; compact?: boolean }) {
	const countdown = useIdleCountdown(session);

	// A worker suspended AFTER its PR merged has its own affordance (Continue /
	// Close via MergeSuspendChip) — don't also render the idle "Paused" chip for it.
	if (isMergeSuspended(session)) return null;

	if (session.isSuspended) {
		const notItsTurn = session.sleepReason === "turn";
		const label = notItsTurn ? "Asleep" : "Paused";
		const title = notItsTurn
			? "Asleep until it is this agent's turn — use Wake to hand it the turn"
			: "Paused to free resources — open to resume";
		if (compact) {
			// The title rides a wrapping span: lucide's icon props do not include one,
			// and the sidebar row is exactly where a reader hovers to ask which kind
			// of sleep this is.
			return (
				<span className="inline-flex shrink-0" title={title}>
					<Moon aria-label={label} className="h-3 w-3 text-passive" strokeWidth={2} />
				</span>
			);
		}
		return (
			<span
				aria-label={label}
				className="inline-flex shrink-0 items-center gap-1 rounded-full border border-[color-mix(in_srgb,var(--fg-passive)_30%,transparent)] px-1.5 py-0.5 text-[10px] font-medium text-passive"
				title={title}
			>
				<Moon className="h-3 w-3" strokeWidth={2} />
				{label}
			</span>
		);
	}

	if (!countdown) return null;
	const title = `Auto-suspends (frees resources; stays on the board) in ${countdown.label}`;
	if (compact) {
		return (
			<span className={cn("shrink-0 font-mono text-[10px] tabular-nums", LEVEL_TEXT[countdown.level])} title={title}>
				{countdown.label}
			</span>
		);
	}
	return (
		<span
			aria-label={`Auto-suspends in ${countdown.label}`}
			className={cn(
				"inline-flex shrink-0 items-center gap-1 rounded-full border px-1.5 py-0.5 font-mono text-[10px] tabular-nums",
				LEVEL_TEXT[countdown.level],
				LEVEL_BORDER[countdown.level],
				countdown.level === "imminent" && "animate-status-pulse",
			)}
			title={title}
		>
			<Clock className="h-3 w-3" strokeWidth={2} />
			{countdown.label}
		</span>
	);
}
