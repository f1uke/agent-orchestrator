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
				// Narrower than the Proc's own clearance, so a long name never reaches
				// the neighbour it would otherwise be mistaken for.
				maxWidth: "132px",
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

export function PetTooltip({ name, sessionId, project, status }: PetTooltipProps) {
	return (
		<div
			data-tooltip
			style={{
				...CHIP,
				padding: "7px 10px",
				font: "500 11px/1.5 ui-sans-serif, system-ui, sans-serif",
				maxWidth: "230px",
				display: "grid",
				gap: "1px",
			}}
		>
			<strong style={{ font: "600 12px/1.4 ui-sans-serif, system-ui, sans-serif" }}>{name || sessionId}</strong>
			<span style={{ color: PROP_COLOURS.bubbleMuted }}>{STATUS_LABELS[status]}</span>
			<span style={{ color: PROP_COLOURS.bubbleMuted, fontFamily: "ui-monospace, monospace", fontSize: "10px" }}>
				{sessionId}
			</span>
			<span style={{ color: PROP_COLOURS.bubbleMuted, fontFamily: "ui-monospace, monospace", fontSize: "10px" }}>
				{project}
			</span>
		</div>
	);
}
