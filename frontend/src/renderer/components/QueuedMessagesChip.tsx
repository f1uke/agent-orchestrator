import { Mail, MailWarning } from "lucide-react";
import type { WorkspaceSession } from "../types/workspace";

/**
 * The board-card / sidebar badge for messages AO is HOLDING for a session.
 *
 * A message addressed to a suspended session cannot be typed into its pane (the
 * tmux is gone), so the daemon queues it and delivers it once the session's
 * agent is listening again. Without this badge that hold is invisible: the
 * sender is told "queued", but the person looking at the board would see a
 * paused card with no hint that anything is waiting inside it.
 *
 * Two states, deliberately distinct:
 *  - waiting: N messages that WILL be delivered.
 *  - undelivered: N that never will (expired attempts, or in flight when the
 *    daemon died). Those are the ones worth acting on, so they read in red.
 *
 * Renders nothing when the inbox is empty, which is almost every session.
 * `compact` shrinks it to a glyph + count for the sidebar row.
 */
export function QueuedMessagesChip({ session, compact = false }: { session: WorkspaceSession; compact?: boolean }) {
	const waiting = session.queuedMessages ?? 0;
	const undelivered = session.queuedMessagesFailed ?? 0;
	if (waiting === 0 && undelivered === 0) return null;

	const failedOnly = waiting === 0;
	const count = failedOnly ? undelivered : waiting;
	const Icon = failedOnly ? MailWarning : Mail;
	const label = failedOnly
		? `${undelivered} message${undelivered === 1 ? "" : "s"} could not be delivered`
		: `${waiting} message${waiting === 1 ? "" : "s"} waiting for this session's agent`;
	const title = failedOnly
		? label
		: `${label}. Held while the session is asleep; delivered once its agent is listening again.`;
	const tone = failedOnly ? "var(--red)" : "var(--fg-passive)";

	if (compact) {
		return (
			<span
				aria-label={label}
				className="inline-flex shrink-0 items-center gap-0.5 text-[10px] tabular-nums"
				style={{ color: tone }}
				title={title}
			>
				<Icon className="h-3 w-3" strokeWidth={2} />
				{count}
			</span>
		);
	}
	return (
		<span
			aria-label={label}
			className="inline-flex shrink-0 items-center gap-1 rounded-full border px-1.5 py-0.5 text-[10px] font-medium tabular-nums"
			style={{ borderColor: `color-mix(in srgb, ${tone} 30%, transparent)`, color: tone }}
			title={title}
		>
			<Icon className="h-3 w-3" strokeWidth={2} />
			{count}
		</span>
	);
}
