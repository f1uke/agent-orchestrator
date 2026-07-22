import { createPortal } from "react-dom";
import { useSplitDragStore, DROP_PANE_ATTR, type DragHover } from "../lib/split-drag";
import type { DropZone } from "../lib/split-layout";

// The share of a pane's edge the move strips occupy — mirrors EDGE_STRIP in
// split-layout so the highlight matches where a drop actually lands.
const EDGE_FRACTION = 0.3;

// The highlight rectangle for a resolved zone, in pane-relative percentages.
// `center` (pane-drag swap) fills the pane; the edge strips mark where the
// dragged/new pane will land.
function zoneRect(zone: DropZone): { left: string; top: string; width: string; height: string } {
	if (zone === "right")
		return { left: `${(1 - EDGE_FRACTION) * 100}%`, top: "0%", width: `${EDGE_FRACTION * 100}%`, height: "100%" };
	if (zone === "down")
		return {
			left: "0%",
			top: `${(1 - EDGE_FRACTION) * 100}%`,
			width: `${(1 - EDGE_FRACTION) * 100}%`,
			height: `${EDGE_FRACTION * 100}%`,
		};
	return { left: "0%", top: "0%", width: "100%", height: "100%" };
}

function zoneLabel(zone: DropZone, kind: "session" | "pane"): string {
	if (zone === "center") return "Swap";
	const verb = kind === "session" ? "Split" : "Move";
	return zone === "right" ? `${verb} right` : `${verb} down`;
}

// The highlight drawn over the pane currently under the pointer.
function DropHighlight({ hover, kind }: { hover: DragHover; kind: "session" | "pane" }) {
	const pane = document.querySelector<HTMLElement>(`[${DROP_PANE_ATTR}="${CSS.escape(hover.sessionId)}"]`);
	if (!pane) return null;
	const rect = pane.getBoundingClientRect();
	const zone = zoneRect(hover.zone);
	return (
		<div
			className="pointer-events-none fixed z-[60]"
			style={{ left: rect.left, top: rect.top, width: rect.width, height: rect.height }}
		>
			<div
				className="absolute rounded-md border-2 border-[var(--accent)] bg-[color-mix(in_srgb,var(--accent)_22%,transparent)] transition-[left,top,width,height] duration-75"
				style={zone}
			>
				<span className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 whitespace-nowrap rounded bg-[var(--accent)] px-2 py-0.5 text-[11px] font-medium text-white shadow">
					{zoneLabel(hover.zone, kind)}
				</span>
			</div>
		</div>
	);
}

/**
 * The overlay rendered while a split drag is in flight. It does three things:
 * a full-window capture layer (pointer-events on) so the xterm panes beneath
 * never receive the drag's pointer moves; the drop-zone highlight on the pane
 * under the pointer; and a ghost chip of the dragged session following the
 * cursor. Idle → renders nothing, so the split view is untouched when not
 * dragging. Portaled to <body> to escape any clipped/scrolling ancestor.
 */
export function SplitDragLayer() {
	const drag = useSplitDragStore((s) => s.drag);
	const pointer = useSplitDragStore((s) => s.pointer);
	const hover = useSplitDragStore((s) => s.hover);
	if (!drag || !pointer) return null;
	return createPortal(
		<div className="fixed inset-0 z-50 cursor-grabbing select-none" style={{ pointerEvents: "auto" }}>
			{hover ? <DropHighlight hover={hover} kind={drag.source.kind} /> : null}
			<div
				className="pointer-events-none fixed z-[70] max-w-[220px] truncate rounded-md border border-border bg-surface px-2.5 py-1 text-[12px] text-foreground shadow-lg"
				style={{ left: pointer.x + 12, top: pointer.y + 12 }}
			>
				{drag.label}
			</div>
		</div>,
		document.body,
	);
}
