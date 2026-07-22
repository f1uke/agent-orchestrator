import type { SessionStatus } from "../renderer/types/workspace";
import { PROCS_INK, PROCS_RIM_PX, PROP_COLOURS } from "./palette";
import { markerForProject, markerPath } from "./project-marker";
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

/**
 * The coordinator's crown, drawn on the LABEL rather than on the character.
 *
 * The human's call (2026-07-22), and the better one: the hat is how you recognise
 * WHICH Proc this is, and taking it away to say what job it holds would have
 * collapsed two readings into one. The chip already answers "what work is this",
 * so the crown belongs there — and the chip's fill changes with it, so the mark
 * is visible before you have read a word of the name.
 */
function LeadCrown() {
	return (
		<svg data-lead-crown width="12" height="9" viewBox="0 0 12 9" style={{ flex: "0 0 auto", display: "block" }}>
			<path
				d="M0.9 8.1 L0.9 1.4 L3.6 4.6 L6 0.9 L8.4 4.6 L11.1 1.4 L11.1 8.1 Z"
				fill={PROP_COLOURS.lead}
				stroke={PROCS_INK}
				strokeWidth="1.4"
				strokeLinejoin="round"
			/>
		</svg>
	);
}

/**
 * The project's mark: a small shape in the project's colour, before the name.
 *
 * The human looked at a full overlay and could not tell which pet belonged to
 * which project — the look is assigned per SESSION, so it carries no project
 * signal at all, and the only project information on screen was inside a hover
 * card you have to ask for.
 *
 * SHAPE as well as colour, which is what the human picked over a plain dot after
 * seeing both: with six colours and no shapes, two of the seven projects in the
 * sample already collided. Shape also survives a greyscale screenshot, 12px, and
 * a colour-vision difference — colour alone survives none of the three.
 */
function ProjectMark({ project }: { project: string }) {
	const mark = markerForProject(project);
	return (
		<svg
			data-project-mark={mark.id}
			width="12"
			height="12"
			viewBox="0 0 12 12"
			style={{ flex: "0 0 auto", display: "block" }}
			aria-hidden
		>
			<path d={markerPath(mark.shape)} fill={mark.fill} stroke={PROCS_INK} strokeWidth="1.4" strokeLinejoin="round" />
		</svg>
	);
}

export function NameTag({
	name,
	lead = false,
	project,
}: {
	name: string;
	lead?: boolean;
	/** Which project this session belongs to. No project, no mark. */
	project?: string;
}) {
	const trimmed = name.trim();
	// Nothing rather than an empty chip: a Proc with no name is just a Proc.
	if (!trimmed) return null;

	return (
		<div
			data-name-tag
			data-lead={lead || undefined}
			style={{
				...CHIP,
				// A different fill, not a different rim: the ink rim is what carries the
				// chip on a LIGHT wallpaper, and swapping it out for gold would trade a
				// readable mark for an unreadable label on half the desktops out there.
				background: lead ? PROP_COLOURS.lead : CHIP.background,
				display: "flex",
				alignItems: "center",
				gap: "4px",
				padding: "1px 6px",
				font: "600 10px/1.4 ui-sans-serif, system-ui, sans-serif",
				// Wider than the figure — it was being squeezed to the 93px figure width
				// and truncating almost every name — but still inside the crowding
				// clearance, so it can never reach the neighbour it would be mistaken for.
				maxWidth: "148px",
				whiteSpace: "nowrap",
			}}
		>
			{lead ? <LeadCrown /> : null}
			<span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{trimmed}</span>
			{/* AFTER the name, not before it. In front, the mark and the coordinator's
			    crown crowd each other and read as one cluttered badge — the human's
			    call once both were on the same chip. */}
			{project ? <ProjectMark project={project} /> : null}
		</div>
	);
}

export type PetTooltipProps = {
	name: string;
	sessionId: string;
	project: string;
	status: SessionStatus;
	lead?: boolean;
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

export function PetTooltip({ name, sessionId, project, status, lead = false }: PetTooltipProps) {
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
			<strong
				style={{
					font: "600 12px/1.4 ui-sans-serif, system-ui, sans-serif",
					display: "flex",
					alignItems: "center",
					gap: "5px",
				}}
			>
				{lead ? <LeadCrown /> : null}
				{name || sessionId}
			</strong>
			<span style={{ color: PROP_COLOURS.bubbleMuted }}>
				{lead ? `Orchestrator · ${STATUS_LABELS[status]}` : STATUS_LABELS[status]}
			</span>
			<span style={IDENTIFIER}>{sessionId.startsWith("@") ? sessionId : `@${sessionId}`}</span>
			<span style={IDENTIFIER}>{project}</span>
		</div>
	);
}
