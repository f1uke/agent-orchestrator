import type { SessionStatus } from "../renderer/types/workspace";
import { PROCS_INK, PROCS_RIM_PX, PROP_COLOURS } from "./palette";
import { STATUS_LABELS } from "./preview";

// The name under a Proc, and the tooltip behind it.
//
// The human's complaint was "ไม่รู้เลยตัวไหนงานอะไร" — no way to tell which Proc is
// which job. The name answers it at a glance; the tooltip answers the follow-up
// (which session, which project) for whoever hovers long enough to ask.
//
// Both sit on the user's WALLPAPER, so both get the same treatment as the pets and
// the bubble: a self-contained fill plus the 2.4px ink rim, never an app theme
// token. There is no light/dark variant, because there is no theme out there.

const CHIP: React.CSSProperties = {
	background: PROP_COLOURS.paper,
	border: `${PROCS_RIM_PX}px solid ${PROCS_INK}`,
	borderColor: PROCS_INK,
	color: PROCS_INK,
	borderRadius: "8px",
};

export function NameTag({ name }: { name: string }) {
	const trimmed = name.trim();
	// Nothing rather than an empty chip: a Proc with no name is just a Proc.
	if (!trimmed) return null;

	return (
		<div
			data-name-tag
			style={{
				...CHIP,
				padding: "1px 6px",
				font: "600 10px/1.4 ui-sans-serif, system-ui, sans-serif",
				// Wider than the figure — it was being squeezed to the 93px figure width
				// and truncating almost every name — but still inside the crowding
				// clearance, so it can never reach the neighbour it would be mistaken for.
				maxWidth: "148px",
				whiteSpace: "nowrap",
				overflow: "hidden",
				textOverflow: "ellipsis",
			}}
		>
			{trimmed}
		</div>
	);
}

export type PetTooltipProps = {
	name: string;
	sessionId: string;
	project: string;
	status: SessionStatus;
};

/**
 * The monospace lines: a session id and a project name.
 *
 * Both are IDENTIFIERS, and CSS treats a hyphen inside one as a break opportunity
 * — which turned "agent-orchestrator-105" into "agent-" above "orchestrator-105"
 * and read as two separate things. An identifier stays on one line and ellipsizes
 * if it must; only the human-written name above it wraps, at its spaces, which is
 * where a reader expects a name to break.
 */
const IDENTIFIER: React.CSSProperties = {
	color: PROP_COLOURS.bubbleMuted,
	fontFamily: "ui-monospace, monospace",
	fontSize: "10px",
	whiteSpace: "nowrap",
	overflow: "hidden",
	textOverflow: "ellipsis",
};

export function PetTooltip({ name, sessionId, project, status }: PetTooltipProps) {
	return (
		<div
			data-tooltip
			style={{
				...CHIP,
				padding: "7px 10px",
				font: "500 11px/1.5 ui-sans-serif, system-ui, sans-serif",
				// Wide enough for the longest identifier it has to hold on one line —
				// "@agent-orchestrator-105" measures 246px — rather than the width that
				// made it wrap. Border-box so this number is the card, not its innards.
				boxSizing: "border-box",
				maxWidth: "292px",
				display: "grid",
				gap: "1px",
			}}
		>
			<strong style={{ font: "600 12px/1.4 ui-sans-serif, system-ui, sans-serif" }}>{name || sessionId}</strong>
			<span style={{ color: PROP_COLOURS.bubbleMuted }}>{STATUS_LABELS[status]}</span>
			<span style={IDENTIFIER}>{sessionId.startsWith("@") ? sessionId : `@${sessionId}`}</span>
			<span style={IDENTIFIER}>{project}</span>
		</div>
	);
}
