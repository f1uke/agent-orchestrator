import { usePrefersReducedMotion } from "../hooks/usePrefersReducedMotion";
import type { WorkspaceSession } from "../types/workspace";
import { statusGlyph } from "../lib/status-glyph";
import { cn } from "../lib/utils";

// Session status glyph: the silhouette of the session's exact status, tinted by
// its lane hue. Shape leads and colour only reinforces, so a list is scannable
// without separating the hues. It reads from the one shared status→glyph map
// (lib/status-glyph) that the board's card gutter also uses, so a session looks
// the same in the sidebar and on the board. Shared by the sidebar rows, the
// split-pane toolbars, and the split session picker.
export function SessionGlyph({ session }: { session: WorkspaceSession }) {
	const { Icon, filled, lane } = statusGlyph(session);
	// The glyph gently breathes (opacity pulse, the shared 1.8s status-pulse) ONLY
	// while the session is actively working, so a live worker is glanceable in the
	// list; every other lane keeps a static glyph. Disabled under reduced-motion.
	const prefersReducedMotion = usePrefersReducedMotion();
	const breathe = lane.key === "working" && !prefersReducedMotion;
	return (
		<span
			aria-hidden="true"
			data-session-glyph={session.status}
			className="flex w-4 shrink-0 items-center justify-center"
			style={{ color: lane.dotVar }}
		>
			<Icon
				className={cn("h-[13px] w-[13px]", breathe && "animate-status-pulse")}
				style={{
					filter: `drop-shadow(0 0 5px color-mix(in srgb, ${lane.dotVar} 70%, transparent))`,
					...(filled ? { fill: "currentColor" } : {}),
				}}
				aria-hidden="true"
			/>
		</span>
	);
}
