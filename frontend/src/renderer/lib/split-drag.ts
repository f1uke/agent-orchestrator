// Pointer-based drag-and-drop for the split view.
//
// Why pointer events, not HTML5 native DnD: the drop targets are terminal
// panes backed by an xterm WebGL canvas. Native `dragover`/`drop` over a
// canvas is unreliable, the native drag image is unstyleable, and the canvas
// captures the mouse for text selection. A pointer gesture with a full-window
// capture overlay is deterministic: once a drag begins, an overlay above every
// pane swallows all pointer events so the terminal underneath never sees them,
// and hit-testing is done against pane rects (not elementFromPoint), so the
// overlay's own z-order is irrelevant.
//
// The gesture state that React must react to (is a drag active, where is the
// pointer, which zone is hovered) lives in a tiny zustand store. The gesture
// bookkeeping (start point, threshold, listeners) is module-local and
// imperative. The DROP itself is applied by a single handler SessionView
// registers, because only it holds the project id + layout writer.

import { create } from "zustand";
import { resolveDropZone, type DragSource, type DropZone } from "./split-layout";

/** Attribute a droppable pane carries: `data-drop-pane="<sessionId>"`. */
export const DROP_PANE_ATTR = "data-drop-pane";

export type DragHover = { sessionId: string; zone: DropZone };
export type ActiveDrag = { source: DragSource; label: string };

type SplitDragState = {
	/** The active drag, or null when idle. Sources subscribe to just this. */
	drag: ActiveDrag | null;
	/** Live pointer position (client coords). Only the drag layer subscribes. */
	pointer: { x: number; y: number } | null;
	/** The pane + zone currently under the pointer, or null between panes. */
	hover: DragHover | null;
	begin: (drag: ActiveDrag, pointer: { x: number; y: number }) => void;
	move: (pointer: { x: number; y: number }, hover: DragHover | null) => void;
	end: () => void;
};

export const useSplitDragStore = create<SplitDragState>((set) => ({
	drag: null,
	pointer: null,
	hover: null,
	begin: (drag, pointer) => set({ drag, pointer, hover: null }),
	move: (pointer, hover) => set({ pointer, hover }),
	end: () => set({ drag: null, pointer: null, hover: null }),
}));

// The drop handler SessionView registers. A single slot: only one session view
// is mounted at a time, and the most recent registration wins.
type DropHandler = (source: DragSource, targetSessionId: string, zone: DropZone) => void;
let dropHandler: DropHandler | null = null;

export function registerSplitDropHandler(handler: DropHandler): () => void {
	dropHandler = handler;
	return () => {
		if (dropHandler === handler) dropHandler = null;
	};
}

// Pixels the pointer must travel before a press becomes a drag, so a plain
// click on a draggable source (a sidebar row navigates, a pane header focuses)
// still behaves as a click.
const DRAG_THRESHOLD_PX = 5;

// Gesture bookkeeping (module-local, imperative).
let gesture: {
	drag: ActiveDrag;
	startX: number;
	startY: number;
	started: boolean;
	pointerId: number;
} | null = null;
// Set on the pointerup that ends a real drag, read once by the source's click
// handler so the click that follows the gesture is suppressed. Cleared on the
// next tick so a genuine later click is unaffected.
let suppressNextClick = false;

/** Hit-test the droppable panes for the pane + zone under a client point. */
function hitTest(clientX: number, clientY: number, kind: DragSource["kind"]): DragHover | null {
	const panes = document.querySelectorAll<HTMLElement>(`[${DROP_PANE_ATTR}]`);
	for (const pane of panes) {
		const rect = pane.getBoundingClientRect();
		if (clientX < rect.left || clientX > rect.right || clientY < rect.top || clientY > rect.bottom) continue;
		const relX = rect.width === 0 ? 0.5 : (clientX - rect.left) / rect.width;
		const relY = rect.height === 0 ? 0.5 : (clientY - rect.top) / rect.height;
		const sessionId = pane.getAttribute(DROP_PANE_ATTR);
		if (!sessionId) continue;
		return { sessionId, zone: resolveDropZone(relX, relY, kind) };
	}
	return null;
}

function onPointerMove(event: PointerEvent) {
	if (!gesture || event.pointerId !== gesture.pointerId) return;
	const store = useSplitDragStore.getState();
	if (!gesture.started) {
		const dx = event.clientX - gesture.startX;
		const dy = event.clientY - gesture.startY;
		if (Math.hypot(dx, dy) < DRAG_THRESHOLD_PX) return;
		gesture.started = true;
		document.body.style.userSelect = "none";
		store.begin(gesture.drag, { x: event.clientX, y: event.clientY });
	}
	// The capture overlay renders once the drag has begun; prevent the terminal
	// (or a text selection) from reacting to intermediate moves regardless.
	event.preventDefault();
	store.move({ x: event.clientX, y: event.clientY }, hitTest(event.clientX, event.clientY, gesture.drag.source.kind));
}

function endGesture(applyDrop: boolean, clientX?: number, clientY?: number) {
	if (!gesture) return;
	const current = gesture;
	window.removeEventListener("pointermove", onPointerMove, true);
	window.removeEventListener("pointerup", onPointerUp, true);
	window.removeEventListener("pointercancel", onPointerCancel, true);
	document.body.style.userSelect = "";
	gesture = null;
	if (!current.started) {
		// Never crossed the threshold: a plain click, leave it to fire normally.
		useSplitDragStore.getState().end();
		return;
	}
	suppressNextClick = true;
	// Clear on the next macrotask, after the click that follows this pointerup.
	window.setTimeout(() => {
		suppressNextClick = false;
	}, 0);
	if (applyDrop && clientX !== undefined && clientY !== undefined) {
		const hover = hitTest(clientX, clientY, current.drag.source.kind);
		if (hover && dropHandler) dropHandler(current.drag.source, hover.sessionId, hover.zone);
	}
	useSplitDragStore.getState().end();
}

function onPointerUp(event: PointerEvent) {
	if (!gesture || event.pointerId !== gesture.pointerId) return;
	endGesture(true, event.clientX, event.clientY);
}

function onPointerCancel(event: PointerEvent) {
	if (!gesture || event.pointerId !== gesture.pointerId) return;
	endGesture(false);
}

/**
 * Begin tracking a potential drag from a source element's `pointerdown`. The
 * drag only actually starts once the pointer passes the threshold, so a click
 * on the same element still works. Ignores non-primary buttons.
 */
export function startSplitDrag(source: DragSource, label: string, event: React.PointerEvent): void {
	if (event.button !== 0 || gesture) return;
	gesture = {
		drag: { source, label },
		startX: event.clientX,
		startY: event.clientY,
		started: false,
		pointerId: event.pointerId,
	};
	window.addEventListener("pointermove", onPointerMove, true);
	window.addEventListener("pointerup", onPointerUp, true);
	window.addEventListener("pointercancel", onPointerCancel, true);
}

/**
 * True once immediately after a drag gesture ends, so the click that a
 * pointerup synthesizes on the source can be suppressed (a dragged sidebar row
 * must not also navigate). Consumes the flag.
 */
export function consumeSplitDragClick(): boolean {
	if (!suppressNextClick) return false;
	suppressNextClick = false;
	return true;
}
