import {
	Check,
	Circle,
	CircleCheck,
	CircleDashed,
	CircleHelp,
	CircleOff,
	Eye,
	GitMerge,
	GitPullRequest,
	GitPullRequestDraft,
	MessageSquare,
	OctagonX,
	SignalZero,
	TriangleAlert,
	type LucideIcon,
} from "lucide-react";
import { attentionZone, primaryPR, type SessionStatus, type WorkspaceSession } from "../types/workspace";
import { prKindLabel, providerFromPRURL } from "./pr-display";
import { laneForZone, type LaneConfig } from "./lane-indicator";

/**
 * The one status→appearance mapping in the app.
 *
 * Replaces the retired coloured edge bars (column top rails, card left accents).
 * Those bars painted the *lane*, which is both redundant — a card already sits
 * inside its lane's column — and coarser than the fact worth showing: the NEEDS
 * YOU lane alone holds four genuinely different statuses (input needed, no
 * signal, CI failed, changes requested) that one coral bar flattened into one.
 *
 * So the shape is bound to the **status**, not the lane, and every status owns a
 * silhouette that survives being shrunk, desaturated, or seen from across the
 * room: a speech bubble is not an octagon is not a warning triangle. Colour only
 * reinforces it (the lane hue), and the visible status text carries the same
 * fact for screen readers and for anyone who cannot separate the hues — so the
 * status is conveyed on three independent channels, never on colour alone.
 *
 * Keep this the ONLY place that maps a status to a glyph or a label: two copies
 * of this mapping is exactly the bug a mutation test cannot see.
 */
export type StatusGlyph = {
	/** Silhouette for this exact status. */
	Icon: LucideIcon;
	/** Paint the glyph solid — reserved for a genuinely live agent. */
	filled: boolean;
	/** Lane the status belongs to, supplying the hue and the column it sorts into. */
	lane: LaneConfig;
	/** Human-readable status, rendered as visible text beside the glyph. */
	label: string;
};

const ICONS: Record<SessionStatus, { Icon: LucideIcon; filled?: boolean }> = {
	// Queued, nothing running yet — a dashed outline reads as "not real work yet".
	todo: { Icon: CircleDashed },
	// A live agent — the only solid glyph, so "something is happening" is the
	// heaviest mark on the board.
	working: { Icon: Circle, filled: true },
	idle: { Icon: Circle, filled: true },
	// ── The four NEEDS YOU statuses. Deliberately four unlike silhouettes:
	// bubble / bars / octagon / triangle stay apart at 13px and in greyscale.
	needs_input: { Icon: MessageSquare },
	no_signal: { Icon: SignalZero },
	ci_failed: { Icon: OctagonX },
	changes_requested: { Icon: TriangleAlert },
	// ── Waiting on someone else.
	review_pending: { Icon: Eye },
	pr_open: { Icon: GitPullRequest },
	draft: { Icon: GitPullRequestDraft },
	unknown: { Icon: CircleHelp },
	// ── One click from done.
	approved: { Icon: CircleCheck },
	mergeable: { Icon: Check },
	// ── Terminal.
	merged: { Icon: GitMerge },
	terminated: { Icon: CircleOff },
};

/**
 * Status label. "PR"/"MR" follows the session's own primary change request;
 * every other status is provider-neutral.
 */
export function statusLabel(session: WorkspaceSession): string {
	const kind = prKindLabel(providerFromPRURL(primaryPR(session)?.url));
	switch (session.status) {
		case "todo":
			return "Queued";
		case "needs_input":
			return "Input needed";
		case "no_signal":
			return "No signal";
		case "ci_failed":
			return "CI failed";
		case "changes_requested":
			return "Changes requested";
		case "review_pending":
			return "Review pending";
		case "draft":
			return `Draft ${kind}`;
		case "pr_open":
			return `${kind} open`;
		case "approved":
			return "Approved";
		case "mergeable":
			return "Ready";
		case "merged":
			return "Merged";
		case "terminated":
			return "Terminated";
		default:
			return "Working";
	}
}

export function statusGlyph(session: WorkspaceSession): StatusGlyph {
	const icon = ICONS[session.status] ?? ICONS.unknown;
	return {
		Icon: icon.Icon,
		filled: icon.filled === true,
		lane: laneForZone(attentionZone(session)),
		label: statusLabel(session),
	};
}
