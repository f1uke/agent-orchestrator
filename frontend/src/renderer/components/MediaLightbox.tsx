import { useCallback, useEffect, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { ChevronLeft, ChevronRight, RotateCcw, X, ZoomIn, ZoomOut } from "lucide-react";
import { PALETTE as P, isVideoMime } from "../lib/smoke-test";

/**
 * One media item the thumbnail + lightbox render: `src` is the daemon URL the
 * bytes are fetched from (rendered via a blob: object URL — a direct
 * http://127.0.0.1 subresource is CSP-blocked on the app:// scheme). `mime`
 * decides image vs video; `filename` is the accessible label. Shared by the
 * smoke-test evidence view and the Jira description media previews.
 */
export type MediaItem = { id: string; filename?: string; mime?: string; src: string };

/**
 * Fetch bytes (connect-src) and expose them as a `blob:` object URL. The renderer
 * runs on the secure `app://` scheme, where a direct <img>/<video src=http://127.0.0.1…>
 * subresource is CSP-blocked (loopback http lives only in connect-src, not
 * img-src/media-src). So both the thumbnail and the lightbox load bytes this way —
 * the blob URL is allowed by img-src/media-src (see src/shared/renderer-csp.ts).
 * Revokes on unmount / src change; reports fetch failure so callers can show a
 * framed placeholder rather than a broken element.
 */
export function useObjectUrl(src: string): { url: string | null; failed: boolean } {
	const [url, setUrl] = useState<string | null>(null);
	const [failed, setFailed] = useState(false);
	useEffect(() => {
		if (!src) return;
		let alive = true;
		let objectUrl: string | null = null;
		setUrl(null);
		setFailed(false);
		fetch(src)
			.then((r) => (r.ok ? r.blob() : Promise.reject(new Error(`media ${r.status}`))))
			.then((blob) => {
				if (!alive) return;
				objectUrl = URL.createObjectURL(blob);
				setUrl(objectUrl);
			})
			.catch(() => {
				if (alive) setFailed(true);
			});
		return () => {
			alive = false;
			if (objectUrl) URL.revokeObjectURL(objectUrl);
		};
	}, [src]);
	return { url, failed };
}

/**
 * A single media thumbnail: fetches `item.src` through the CORS-gated fetch and
 * renders an <img>/<video> from a blob: URL (a direct loopback-http subresource is
 * CSP-blocked on app://). A fetch failure shows a framed placeholder, never a
 * broken element. `style` sizes the media element (callers frame it). When
 * `onOpen` is given (and the bytes loaded) the thumbnail is a button that hands
 * back its own element so the opener can restore focus on close.
 */
export function MediaThumb({
	item,
	onOpen,
	style,
	className,
	onLoadState,
}: {
	item: MediaItem;
	onOpen?: (trigger: HTMLElement) => void;
	style: React.CSSProperties;
	className?: string;
	// Notifies the framing parent of load outcome so it can gate its own chrome
	// (e.g. the smoke view hides its reveal/open bar when the bytes fail to load).
	onLoadState?: (state: { failed: boolean; loaded: boolean }) => void;
}) {
	const { url, failed } = useObjectUrl(item.src);
	const openable = Boolean(onOpen) && !failed;
	useEffect(() => {
		onLoadState?.({ failed, loaded: Boolean(url) });
	}, [failed, url, onLoadState]);

	const media = failed ? (
		<div
			style={{
				...style,
				display: "flex",
				alignItems: "center",
				justifyContent: "center",
				padding: 4,
				fontSize: 10,
				lineHeight: 1.3,
				textAlign: "center",
				color: P.muted2,
				overflow: "hidden",
			}}
			aria-label={item.filename || "media"}
		>
			{item.filename || "couldn't load"}
		</div>
	) : !url ? (
		// Loading the bytes — a framed placeholder avoids a broken-image flash.
		<div style={style} aria-label={item.filename || "media"} />
	) : isVideoMime(item.mime ?? "") ? (
		<video src={url} style={style} muted controls={false} playsInline aria-label={item.filename || "clip"} />
	) : (
		<img src={url} alt={item.filename || "media"} style={style} />
	);

	return (
		<div
			role={openable ? "button" : undefined}
			tabIndex={openable ? 0 : undefined}
			aria-label={openable ? `View ${item.filename || "media"}` : undefined}
			className={className}
			onClick={openable ? (e) => onOpen!(e.currentTarget) : undefined}
			onKeyDown={
				openable
					? (e) => {
							if (e.key === "Enter" || e.key === " ") {
								e.preventDefault();
								onOpen!(e.currentTarget);
							}
						}
					: undefined
			}
			style={{ display: "block", borderRadius: 8, outline: "none", cursor: openable ? "zoom-in" : "default" }}
		>
			{media}
		</div>
	);
}

const ZOOM_MIN = 1;
const ZOOM_MAX = 4;
const ZOOM_STEP = 1.5;

function clamp(v: number, lo: number, hi: number): number {
	return Math.min(hi, Math.max(lo, v));
}

/**
 * Centered, full-screen modal viewer for a set of media items. Built on the app's
 * Radix Dialog primitive, so focus trap, Esc-to-close, body-scroll lock, focus
 * restore, and role=dialog/aria-modal all come for free and match the app's other
 * modals. The Content fills the viewport (transparent) over a dimmed Overlay;
 * clicking the padding around the media (target === currentTarget) closes, while
 * clicking the media itself does not. Images support wheel/pinch + button +
 * double-click zoom with drag-to-pan; videos play inline with standard controls.
 * Left/Right (keys and arrows) page between items, wrapping at the ends. Zoom/pan
 * reset whenever the shown item changes.
 */
export function MediaLightbox({
	items,
	index,
	onIndexChange,
	onClose,
	triggerRef,
}: {
	items: MediaItem[];
	index: number;
	onIndexChange: (next: number) => void;
	onClose: () => void;
	triggerRef: React.MutableRefObject<HTMLElement | null>;
}) {
	const count = items.length;
	const safeIndex = count > 0 ? clamp(index, 0, count - 1) : 0;
	const current = items[safeIndex];

	const [scale, setScale] = useState(1);
	const [pan, setPan] = useState({ x: 0, y: 0 });
	const [dragging, setDragging] = useState(false);
	const viewportRef = useRef<HTMLDivElement | null>(null);
	// Latest scale/pan for the wheel + pan math (event closures would go stale).
	const scaleRef = useRef(1);
	const panRef = useRef({ x: 0, y: 0 });
	const dragRef = useRef<{ x: number; y: number; px: number; py: number } | null>(null);
	scaleRef.current = scale;
	panRef.current = pan;

	const { url, failed } = useObjectUrl(current ? current.src : "");
	const isVideo = current ? isVideoMime(current.mime ?? "") : false;
	const zoomable = Boolean(url) && !failed && !isVideo;

	// Reset zoom/pan whenever the shown item changes (and on first open).
	useEffect(() => {
		setScale(1);
		setPan({ x: 0, y: 0 });
	}, [safeIndex]);

	// If the set empties out from under us, there is nothing to show.
	useEffect(() => {
		if (count === 0) onClose();
	}, [count, onClose]);

	const goto = useCallback(
		(next: number) => {
			if (count <= 1) return;
			onIndexChange(((next % count) + count) % count); // wrap at both ends
		},
		[count, onIndexChange],
	);

	const clampPan = useCallback((p: { x: number; y: number }, s: number) => {
		const el = viewportRef.current;
		const maxX = ((s - 1) * (el?.clientWidth ?? 0)) / 2;
		const maxY = ((s - 1) * (el?.clientHeight ?? 0)) / 2;
		return { x: clamp(p.x, -maxX, maxX), y: clamp(p.y, -maxY, maxY) };
	}, []);

	// Zoom to `nextScale`, keeping the content point under (dx,dy) — an offset from
	// the viewport center — fixed. Buttons pass no anchor (zoom about center).
	const zoomTo = useCallback(
		(nextScale: number, anchor?: { dx: number; dy: number }) => {
			const s = scaleRef.current;
			const s2 = clamp(nextScale, ZOOM_MIN, ZOOM_MAX);
			if (s2 === 1) {
				setScale(1);
				setPan({ x: 0, y: 0 });
				return;
			}
			const a = anchor ?? { dx: 0, dy: 0 };
			const p = panRef.current;
			const np = clampPan({ x: a.dx - ((a.dx - p.x) / s) * s2, y: a.dy - ((a.dy - p.y) / s) * s2 }, s2);
			setScale(s2);
			setPan(np);
		},
		[clampPan],
	);

	// Native wheel listener so it can preventDefault (React onWheel is passive).
	useEffect(() => {
		const el = viewportRef.current;
		if (!el || !zoomable) return;
		const onWheel = (e: WheelEvent) => {
			e.preventDefault();
			const rect = el.getBoundingClientRect();
			const dx = e.clientX - (rect.left + rect.width / 2);
			const dy = e.clientY - (rect.top + rect.height / 2);
			zoomTo(scaleRef.current * Math.exp(-e.deltaY * 0.0015), { dx, dy });
		};
		el.addEventListener("wheel", onWheel, { passive: false });
		return () => el.removeEventListener("wheel", onWheel);
	}, [zoomable, zoomTo, safeIndex]);

	const onDoubleClick = (e: React.MouseEvent) => {
		if (!zoomable) return;
		const el = viewportRef.current;
		if (!el) return;
		if (scaleRef.current > 1) {
			zoomTo(1);
			return;
		}
		const rect = el.getBoundingClientRect();
		zoomTo(2.5, { dx: e.clientX - (rect.left + rect.width / 2), dy: e.clientY - (rect.top + rect.height / 2) });
	};

	const onPointerDown = (e: React.PointerEvent) => {
		if (scaleRef.current <= 1 || !zoomable) return;
		e.currentTarget.setPointerCapture?.(e.pointerId);
		dragRef.current = { x: panRef.current.x, y: panRef.current.y, px: e.clientX, py: e.clientY };
		setDragging(true);
	};
	const onPointerMove = (e: React.PointerEvent) => {
		const d = dragRef.current;
		if (!d) return;
		setPan(clampPan({ x: d.x + (e.clientX - d.px), y: d.y + (e.clientY - d.py) }, scaleRef.current));
	};
	const endDrag = (e: React.PointerEvent) => {
		if (!dragRef.current) return;
		e.currentTarget.releasePointerCapture?.(e.pointerId);
		dragRef.current = null;
		setDragging(false);
	};

	const onKeyDown = (e: React.KeyboardEvent) => {
		if (e.key === "ArrowLeft") {
			e.preventDefault();
			goto(safeIndex - 1);
		} else if (e.key === "ArrowRight") {
			e.preventDefault();
			goto(safeIndex + 1);
		}
	};

	return (
		<Dialog.Root open onOpenChange={(o) => !o && onClose()}>
			<Dialog.Portal>
				<Dialog.Overlay
					style={{
						position: "fixed",
						inset: 0,
						zIndex: 1000,
						background: "rgba(6,6,8,.82)",
						backdropFilter: "blur(2px)",
					}}
				/>
				<Dialog.Content
					aria-label={`Evidence viewer${current?.filename ? `: ${current.filename}` : ""}`}
					aria-describedby={undefined}
					onKeyDown={onKeyDown}
					onCloseAutoFocus={(e) => {
						// Return focus to the thumbnail that opened the viewer.
						e.preventDefault();
						triggerRef.current?.focus?.();
					}}
					onClick={(e) => {
						if (e.target === e.currentTarget) onClose();
					}}
					style={{
						position: "fixed",
						inset: 0,
						zIndex: 1001,
						display: "flex",
						alignItems: "center",
						justifyContent: "center",
						padding: "56px 72px",
						outline: "none",
						border: "none",
						background: "transparent",
					}}
				>
					<Dialog.Title style={SR_ONLY}>Evidence viewer</Dialog.Title>

					<button type="button" aria-label="Close viewer" title="Close (Esc)" onClick={onClose} style={cornerBtn("top")}>
						<X size={18} aria-hidden="true" />
					</button>

					{count > 1 && (
						<>
							<span aria-hidden="true" style={counterStyle}>
								{safeIndex + 1} / {count}
							</span>
							<button
								type="button"
								aria-label="Previous evidence"
								title="Previous (←)"
								onClick={() => goto(safeIndex - 1)}
								style={edgeBtn("left")}
							>
								<ChevronLeft size={26} aria-hidden="true" />
							</button>
							<button
								type="button"
								aria-label="Next evidence"
								title="Next (→)"
								onClick={() => goto(safeIndex + 1)}
								style={edgeBtn("right")}
							>
								<ChevronRight size={26} aria-hidden="true" />
							</button>
						</>
					)}

					<div
						ref={viewportRef}
						onDoubleClick={onDoubleClick}
						onPointerDown={onPointerDown}
						onPointerMove={onPointerMove}
						onPointerUp={endDrag}
						onPointerCancel={endDrag}
						style={{
							position: "relative",
							display: "flex",
							alignItems: "center",
							justifyContent: "center",
							maxWidth: "min(90vw, 1400px)",
							maxHeight: "82vh",
							overflow: "hidden",
							borderRadius: 10,
							touchAction: "none",
						}}
					>
						{failed ? (
							<div style={failedBox} aria-label={current?.filename || "media"}>
								Couldn&apos;t load {current?.filename || "this media"}
							</div>
						) : !url ? (
							<div style={{ ...failedBox, color: P.muted }} aria-live="polite">
								Loading…
							</div>
						) : isVideo ? (
							<video
								src={url}
								controls
								autoPlay
								muted
								playsInline
								aria-label={current?.filename || "clip"}
								style={{
									maxWidth: "min(90vw, 1400px)",
									maxHeight: "82vh",
									borderRadius: 10,
									background: "#000",
									display: "block",
								}}
							/>
						) : (
							<img
								src={url}
								alt={current?.filename || "media"}
								draggable={false}
								style={{
									maxWidth: "min(90vw, 1400px)",
									maxHeight: "82vh",
									objectFit: "contain",
									display: "block",
									userSelect: "none",
									transform: `translate(${pan.x}px, ${pan.y}px) scale(${scale})`,
									transformOrigin: "center center",
									transition: dragging ? "none" : "transform .1s ease-out",
									cursor: scale > 1 ? (dragging ? "grabbing" : "grab") : "zoom-in",
									willChange: "transform",
								}}
							/>
						)}
					</div>

					{zoomable && (
						<div style={zoomBarStyle}>
							<button
								type="button"
								aria-label="Zoom out"
								title="Zoom out"
								onClick={() => zoomTo(scale / ZOOM_STEP)}
								disabled={scale <= ZOOM_MIN}
								style={zoomBtn(scale <= ZOOM_MIN)}
							>
								<ZoomOut size={16} aria-hidden="true" />
							</button>
							<span aria-hidden="true" style={{ minWidth: 42, textAlign: "center", fontSize: 12, color: P.body }}>
								{Math.round(scale * 100)}%
							</span>
							<button
								type="button"
								aria-label="Zoom in"
								title="Zoom in"
								onClick={() => zoomTo(scale * ZOOM_STEP)}
								disabled={scale >= ZOOM_MAX}
								style={zoomBtn(scale >= ZOOM_MAX)}
							>
								<ZoomIn size={16} aria-hidden="true" />
							</button>
							<button
								type="button"
								aria-label="Reset zoom"
								title="Reset zoom"
								onClick={() => zoomTo(1)}
								disabled={scale === ZOOM_MIN}
								style={zoomBtn(scale === ZOOM_MIN)}
							>
								<RotateCcw size={15} aria-hidden="true" />
							</button>
						</div>
					)}
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}

const SR_ONLY: React.CSSProperties = {
	position: "absolute",
	width: 1,
	height: 1,
	padding: 0,
	margin: -1,
	overflow: "hidden",
	clip: "rect(0 0 0 0)",
	whiteSpace: "nowrap",
	border: 0,
};

const counterStyle: React.CSSProperties = {
	position: "absolute",
	top: 20,
	left: "50%",
	transform: "translateX(-50%)",
	fontSize: 12.5,
	fontWeight: 600,
	color: P.body,
	background: "rgba(15,15,18,.8)",
	border: `1px solid ${P.borderPill}`,
	borderRadius: 999,
	padding: "3px 12px",
};

const failedBox: React.CSSProperties = {
	display: "flex",
	alignItems: "center",
	justifyContent: "center",
	minWidth: 240,
	minHeight: 140,
	padding: 24,
	fontSize: 13,
	textAlign: "center",
	color: P.muted2,
	background: P.cardBg,
	border: `1px solid ${P.borderPill}`,
	borderRadius: 10,
};

const zoomBarStyle: React.CSSProperties = {
	position: "absolute",
	bottom: 22,
	left: "50%",
	transform: "translateX(-50%)",
	display: "flex",
	alignItems: "center",
	gap: 6,
	background: "rgba(15,15,18,.85)",
	border: `1px solid ${P.borderPill}`,
	borderRadius: 999,
	padding: "5px 8px",
	backdropFilter: "blur(4px)",
};

function cornerBtn(_pos: "top"): React.CSSProperties {
	return {
		position: "absolute",
		top: 16,
		right: 16,
		width: 36,
		height: 36,
		borderRadius: "50%",
		display: "flex",
		alignItems: "center",
		justifyContent: "center",
		color: "#fff",
		background: "rgba(15,15,18,.8)",
		border: `1px solid ${P.borderPill}`,
		cursor: "pointer",
		padding: 0,
	};
}

function edgeBtn(side: "left" | "right"): React.CSSProperties {
	return {
		position: "absolute",
		top: "50%",
		[side]: 16,
		transform: "translateY(-50%)",
		width: 44,
		height: 44,
		borderRadius: "50%",
		display: "flex",
		alignItems: "center",
		justifyContent: "center",
		color: "#fff",
		background: "rgba(15,15,18,.8)",
		border: `1px solid ${P.borderPill}`,
		cursor: "pointer",
		padding: 0,
	};
}

function zoomBtn(disabled: boolean): React.CSSProperties {
	return {
		width: 30,
		height: 30,
		borderRadius: 8,
		display: "flex",
		alignItems: "center",
		justifyContent: "center",
		color: disabled ? P.muted2 : "#fff",
		background: "transparent",
		border: "none",
		cursor: disabled ? "default" : "pointer",
		padding: 0,
		opacity: disabled ? 0.5 : 1,
	};
}
