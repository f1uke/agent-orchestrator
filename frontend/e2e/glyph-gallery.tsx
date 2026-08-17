import { createRoot } from "react-dom/client";
import { statusGlyph } from "../src/renderer/lib/status-glyph";
import type { SessionStatus, WorkspaceSession } from "../src/renderer/types/workspace";
import "../src/renderer/styles.css";

// Every SessionStatus, spelled out. The Record below makes the list exhaustive at
// compile time, so adding a status to the union without giving it a gallery row
// (and therefore without ever measuring its glyph) fails `tsc` rather than
// silently shipping an unmeasured mark.
const ALL_STATUSES: Record<SessionStatus, true> = {
	todo: true,
	working: true,
	pr_open: true,
	draft: true,
	ci_failed: true,
	review_pending: true,
	changes_requested: true,
	approved: true,
	mergeable: true,
	merged: true,
	needs_input: true,
	no_signal: true,
	idle: true,
	terminated: true,
	unknown: true,
};

const statuses = Object.keys(ALL_STATUSES) as SessionStatus[];

function sessionFor(status: SessionStatus): WorkspaceSession {
	return {
		id: `gallery-${status}`,
		workspaceId: "gallery",
		workspaceName: "gallery",
		title: status,
		provider: "claude-code",
		branch: `gallery/${status}`,
		status,
		updatedAt: new Date(0).toISOString(),
		prs: [],
	};
}

// The board card's gutter, verbatim (SessionsBoard.tsx): an 18px leading column
// holding the status glyph at 15px. The spec measures what this draws.
function GalleryRow({ status }: { status: SessionStatus }) {
	const { Icon, filled, lane, label } = statusGlyph(sessionFor(status));
	return (
		<div data-glyph-row={status} style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 0" }}>
			<span className="flex w-[18px] shrink-0 justify-center" style={{ color: lane.dotVar }}>
				<Icon className="h-[15px] w-[15px]" style={filled ? { fill: "currentColor" } : undefined} aria-hidden="true" />
			</span>
			<span style={{ color: lane.dotVar, fontSize: 12, fontWeight: 600 }}>{label}</span>
			<span style={{ color: "var(--fg-passive)", fontSize: 11, fontFamily: "monospace" }}>{status}</span>
		</div>
	);
}

createRoot(document.getElementById("root") as HTMLElement).render(
	<div style={{ background: "var(--kanban-card-bg)", minHeight: "100vh", padding: 24 }}>
		{statuses.map((status) => (
			<GalleryRow key={status} status={status} />
		))}
	</div>,
);
