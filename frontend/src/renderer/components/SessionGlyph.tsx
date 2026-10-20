import type { WorkspaceSession } from "../types/workspace";
import { statusGlyph } from "../lib/status-glyph";

// Session status glyph: the silhouette of the session's exact status, tinted by
// its lane hue. Shape leads and colour only reinforces, so a list is scannable
// without separating the hues. It reads from the one shared status→glyph map
// (lib/status-glyph) that the board's card gutter also uses, so a session looks
// the same in the sidebar and on the board. Shared by the sidebar rows, the
// split-pane toolbars, and the split session picker.
export function SessionGlyph({ session }: { session: WorkspaceSession }) {
	const { Icon, filled, lane } = statusGlyph(session);
	return (
		<span
			aria-hidden="true"
			data-session-glyph={session.status}
			className="flex w-4 shrink-0 items-center justify-center"
			style={{ color: lane.dotVar }}
		>
			<Icon
				className="h-[13px] w-[13px]"
				style={{
					filter: `drop-shadow(0 0 5px color-mix(in srgb, ${lane.dotVar} 70%, transparent))`,
					...(filled ? { fill: "currentColor" } : {}),
				}}
				aria-hidden="true"
			/>
		</span>
	);
}
