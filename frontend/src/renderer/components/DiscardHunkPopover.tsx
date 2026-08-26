import { MONO, PALETTE as P, VIEWER as V } from "../lib/comment-inbox";
import type { Hunk } from "../lib/editor/change-lanes";

const KIND_TITLE: Record<Hunk["kind"], string> = {
	added: "Discard this addition?",
	modified: "Discard this change?",
	removed: "Restore what was deleted?",
};

const KIND_LEAD: Record<Hunk["kind"], string> = {
	added: "These lines are not in the last commit. Discarding removes them.",
	modified: "The last commit has these lines instead:",
	removed: "The last commit has these lines here:",
};

/**
 * What a gutter bar opens: what the last commit says about these lines, and only
 * then the red button.
 *
 * 🗝 Xcode does not discard on the first click, and neither does this. A gutter
 * bar is a one-pixel target next to a line number; a click that destroys work
 * without showing it first is the wrong trade for a target that small.
 */
export function DiscardHunkPopover({
	hunk,
	top,
	left,
	onDiscard,
	onDismiss,
}: {
	hunk: Hunk;
	/** Viewport-relative position of the clicked line, inside the editor host. */
	top: number;
	left: number;
	onDiscard: () => void;
	onDismiss: () => void;
}) {
	const preview = hunk.oldText.length > 0 ? hunk.oldText : ["(nothing — these lines are new)"];
	return (
		<div
			data-testid="discard-hunk-popover"
			role="dialog"
			aria-label={KIND_TITLE[hunk.kind]}
			// Monaco parents this node and eats the mousedown, closing the popover
			// before the button ever receives a click.
			onMouseDown={(e) => e.stopPropagation()}
			onClick={(e) => e.stopPropagation()}
			style={{
				position: "absolute",
				top,
				left,
				zIndex: 30,
				width: 360,
				maxWidth: "calc(100% - 32px)",
				background: P.menuBg,
				border: `1px solid ${P.borderMenu}`,
				borderRadius: 10,
				boxShadow: "0 12px 32px rgb(0 0 0 / 0.32)",
				padding: 12,
				display: "flex",
				flexDirection: "column",
				gap: 8,
			}}
		>
			<p style={{ margin: 0, fontSize: 12.5, fontWeight: 600, color: P.textStrong }}>{KIND_TITLE[hunk.kind]}</p>
			<p style={{ margin: 0, fontSize: 11.5, color: P.secondary }}>{KIND_LEAD[hunk.kind]}</p>
			<pre
				style={{
					margin: 0,
					maxHeight: 168,
					overflow: "auto",
					fontFamily: MONO,
					fontSize: 11.5,
					lineHeight: 1.5,
					color: P.body,
					background: V.bg,
					border: `1px solid ${P.borderCard}`,
					borderRadius: 7,
					padding: "7px 9px",
					whiteSpace: "pre",
				}}
			>
				{preview.join("\n")}
			</pre>
			<p style={{ margin: 0, fontSize: 11, color: P.muted3 }}>
				This edits the buffer, so ⌘Z undoes it. Nothing is written until you save.
			</p>
			<div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
				<button
					type="button"
					onClick={onDismiss}
					style={{
						fontSize: 12,
						padding: "5px 11px",
						borderRadius: 7,
						border: `1px solid ${P.borderPill}`,
						background: "transparent",
						color: P.secondary,
						cursor: "pointer",
					}}
				>
					Cancel
				</button>
				<button
					type="button"
					onClick={onDiscard}
					style={{
						fontSize: 12,
						fontWeight: 600,
						padding: "5px 11px",
						borderRadius: 7,
						border: `1px solid color-mix(in oklab, ${P.red} 45%, transparent)`,
						background: `color-mix(in oklab, ${P.red} 14%, transparent)`,
						color: P.red,
						cursor: "pointer",
					}}
				>
					{hunk.kind === "removed" ? "Restore" : "Discard"}
				</button>
			</div>
		</div>
	);
}
