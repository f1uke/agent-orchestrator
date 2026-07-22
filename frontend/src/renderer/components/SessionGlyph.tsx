import { usePrefersReducedMotion } from "../hooks/usePrefersReducedMotion";
import { attentionZone, type WorkspaceSession } from "../types/workspace";
import { laneForZone } from "../lib/lane-indicator";
import { cn } from "../lib/utils";

// Session status glyph: a distinct lane shape (filled dot ● / ring ◎ / half ◐ /
// check ✓) tinted by the lane hue with a soft glow, so a session list is
// scannable by shape AND colour — the same 4-hue semantic system the board
// uses (lib/lane-indicator, design handoff Board.dc.html). Shared by the
// sidebar rows, the split-pane toolbars, and the split session picker.
export function SessionGlyph({ session }: { session: WorkspaceSession }) {
	const lane = laneForZone(attentionZone(session));
	const { Icon } = lane;
	// The glyph gently breathes (opacity pulse, the shared 1.8s status-pulse) ONLY
	// while the session is actively working, so a live worker is glanceable in the
	// list; every other lane keeps a static glyph. Disabled under reduced-motion.
	const prefersReducedMotion = usePrefersReducedMotion();
	const breathe = lane.key === "working" && !prefersReducedMotion;
	return (
		<span aria-hidden="true" className="flex w-4 shrink-0 items-center justify-center" style={{ color: lane.dotVar }}>
			<Icon
				className={cn("h-[13px] w-[13px]", breathe && "animate-status-pulse")}
				style={{
					filter: `drop-shadow(0 0 5px color-mix(in srgb, ${lane.dotVar} 70%, transparent))`,
					...(lane.filled ? { fill: "currentColor" } : {}),
				}}
				aria-hidden="true"
			/>
		</span>
	);
}
