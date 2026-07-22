import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import type { PanelSize } from "react-resizable-panels";
import {
	nearestPaneInDirection,
	paneSessionIds,
	requiredExtent,
	type FocusDirection,
	type SplitBranch,
	type SplitNode,
} from "../lib/split-layout";
import { focusTerminal } from "../lib/terminal-focus";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "./ui/resizable";

type SplitTreeViewProps = {
	root: SplitNode;
	/** The focused pane = the session in the URL; SessionView navigates on change. */
	focusedSessionId: string;
	onFocusPane: (sessionId: string) => void;
	/** A divider drag settled; path addresses the branch (lib/split-layout). */
	onRatioChange: (path: string, ratio: number) => void;
	renderPane: (sessionId: string, focused: boolean) => ReactNode;
};

/**
 * The multi-terminal split region: renders the layout tree as nested resizable
 * panel groups (one per branch, two panels each), one pane per leaf.
 *
 * Sizing: every pane is floored at MIN_PANE_WIDTH×MIN_PANE_HEIGHT (a terminal
 * smaller than that is decoration, not a terminal). When the window cannot fit
 * the whole arrangement at that floor, THIS REGION SCROLLS instead of crushing
 * panes — explicit user decision: the cap (10) bounds how many panes exist,
 * the window merely bounds how many are comfortably visible at once.
 *
 * Focus: exactly one pane is focused (the routed session). Clicking a pane
 * focuses it; ⌘⌥←→↑↓ (or Ctrl+Alt on non-mac) moves focus geometrically —
 * measured from the panes' actual rects, since visual adjacency depends on
 * divider positions the tree alone cannot know.
 */
export function SplitTreeView({ root, focusedSessionId, onFocusPane, onRatioChange, renderPane }: SplitTreeViewProps) {
	const containerRef = useRef<HTMLDivElement | null>(null);
	const extent = requiredExtent(root);

	// Keyboard focus movement. Capture phase so the shortcut wins over the
	// focused xterm's own key handling (xterm swallows plain keys, but a
	// window-capture listener sees them first).
	useEffect(() => {
		const onKeyDown = (event: KeyboardEvent) => {
			if (!(event.metaKey || event.ctrlKey) || !event.altKey) return;
			const direction: FocusDirection | null =
				event.key === "ArrowLeft"
					? "left"
					: event.key === "ArrowRight"
						? "right"
						: event.key === "ArrowUp"
							? "up"
							: event.key === "ArrowDown"
								? "down"
								: null;
			if (!direction) return;
			event.preventDefault();
			const host = containerRef.current;
			if (!host) return;
			const rects = Array.from(host.querySelectorAll<HTMLElement>("[data-split-pane]")).map((el) => {
				const rect = el.getBoundingClientRect();
				return {
					sessionId: el.dataset.splitPane as string,
					rect: { left: rect.left, top: rect.top, width: rect.width, height: rect.height },
				};
			});
			const next = nearestPaneInDirection(rects, focusedSessionId, direction);
			if (next) onFocusPane(next);
		};
		window.addEventListener("keydown", onKeyDown, true);
		return () => window.removeEventListener("keydown", onKeyDown, true);
	}, [focusedSessionId, onFocusPane]);

	// Hand the caret to the newly focused pane's terminal. Runs after the pane's
	// XtermTerminal re-registered itself as the active terminal (child effects
	// run before this parent effect in the same commit).
	useEffect(() => {
		focusTerminal();
	}, [focusedSessionId]);

	return (
		<div ref={containerRef} className="h-full min-h-0 overflow-auto">
			{/* When the container beats the required extent this div is exactly the
			    container; below it, the mins win and the outer div scrolls. */}
			<div className="h-full w-full" style={{ minWidth: extent.width, minHeight: extent.height }}>
				<SplitNodeView
					node={root}
					path=""
					focusedSessionId={focusedSessionId}
					onFocusPane={onFocusPane}
					onRatioChange={onRatioChange}
					renderPane={renderPane}
				/>
			</div>
		</div>
	);
}

type NodeViewProps = {
	node: SplitNode;
	path: string;
	focusedSessionId: string;
	onFocusPane: (sessionId: string) => void;
	onRatioChange: (path: string, ratio: number) => void;
	renderPane: (sessionId: string, focused: boolean) => ReactNode;
};

function SplitNodeView({ node, path, ...rest }: NodeViewProps) {
	if (node.kind === "leaf") {
		return <SplitLeafView sessionId={node.sessionId} {...rest} />;
	}
	// Key by the subtree's pane list: structural changes (add/remove) remount
	// the group so react-resizable-panels re-derives constraints and the frozen
	// defaultSize is re-read — while ratio-only updates keep the mounted group
	// (rrp owns live sizes; see the freeze note in SplitBranchView).
	return <SplitBranchView key={`${path}:${paneSessionIds(node).join("|")}`} node={node} path={path} {...rest} />;
}

function SplitBranchView({ node, path, ...rest }: NodeViewProps & { node: SplitBranch }) {
	const { onRatioChange } = rest;
	const separatorRef = useRef<HTMLDivElement | null>(null);
	// Frozen at mount: rrp re-registers a panel whenever defaultSize's identity
	// changes and that re-registration races imperative sizing within a commit
	// (see SessionView's inspectorDefaultSizeRef). After mount rrp owns the
	// size; the store ratio only matters again at the next structural remount.
	const [initialRatio] = useState(node.ratio);
	const horizontal = node.orientation === "horizontal";
	const firstExtent = requiredExtent(node.first);
	const secondExtent = requiredExtent(node.second);

	// Persist only drag-settled sizes: rrp v4 derives sizes from observed DOM
	// layout, so programmatic/layout-driven resizes fire onResize too; gating on
	// the actively dragged separator keeps those out of the store (same pattern
	// as SessionView's inspector split).
	const handleFirstResize = useCallback(
		(size: PanelSize) => {
			if (separatorRef.current?.getAttribute("data-separator") !== "active") return;
			onRatioChange(path, size.asPercentage / 100);
		},
		[onRatioChange, path],
	);

	return (
		<ResizablePanelGroup id={`split-group-${path || "root"}`} orientation={node.orientation}>
			<ResizablePanel
				defaultSize={`${initialRatio * 100}%`}
				id={`split-${path}f`}
				minSize={horizontal ? firstExtent.width : firstExtent.height}
				onResize={handleFirstResize}
			>
				<SplitNodeView node={node.first} path={`${path}f`} {...rest} />
			</ResizablePanel>
			<ResizableHandle elementRef={separatorRef} />
			<ResizablePanel id={`split-${path}s`} minSize={horizontal ? secondExtent.width : secondExtent.height}>
				<SplitNodeView node={node.second} path={`${path}s`} {...rest} />
			</ResizablePanel>
		</ResizablePanelGroup>
	);
}

function SplitLeafView({
	sessionId,
	focusedSessionId,
	onFocusPane,
	renderPane,
}: Omit<NodeViewProps, "node" | "path" | "onRatioChange"> & { sessionId: string }) {
	const focused = sessionId === focusedSessionId;
	return (
		<div
			className="relative h-full min-h-0 min-w-0"
			data-split-pane={sessionId}
			onMouseDownCapture={(event) => {
				if (focused) return;
				// A press on the pane's own controls (split picker, remove) acts on
				// that pane without moving focus. Focusing here would flip the
				// toolbar from slim to full between mousedown and click, unmounting
				// the pressed button mid-gesture — the click would silently die and
				// the control would need a second press.
				if ((event.target as HTMLElement | null)?.closest("[data-split-pane-controls]")) return;
				onFocusPane(sessionId);
			}}
		>
			{renderPane(sessionId, focused)}
			{/* Focus ring above the pane's content (an inset box-shadow on the
			    wrapper would be painted over by the terminal's own background). */}
			{focused ? (
				<div
					aria-hidden="true"
					className="pointer-events-none absolute inset-0 z-10"
					style={{ boxShadow: "inset 0 0 0 1px var(--accent)" }}
				/>
			) : null}
		</div>
	);
}
