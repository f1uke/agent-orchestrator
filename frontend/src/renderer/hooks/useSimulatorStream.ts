import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { getApiBaseUrl, subscribeApiBaseUrl } from "../lib/api-client";

/**
 * The live iOS Simulator screen, drawn into a canvas.
 *
 * The socket IS the subscription. The daemon starts a capture process for a
 * device when its first viewer connects and kills it when the last one leaves,
 * so this hook's only real job is to be honest about when somebody is looking:
 * it connects when the Simulator tab is showing AND the page is visible AND the
 * window has focus, and closes the moment any of those stops being true. There
 * is no timer and no heartbeat to forget — closing the socket is what stops the
 * capture.
 *
 * Frames are drawn through `createImageBitmap` into a canvas rather than being
 * turned into an object URL per frame. A URL per frame is an allocation per
 * frame for the GC to chase, and it is the one piece of the reference
 * implementation we deliberately did not copy.
 */

export type SimStreamState =
	/** Nobody is looking, so nothing is running. */
	| "paused"
	| "connecting"
	| "live"
	/** The stream ended on its own — the device went away, or capture failed. */
	| "ended";

export type SimStreamStatus = {
	state: SimStreamState;
	/** Why the stream ended, or why it cannot start. Empty while healthy. */
	message: string;
	/** When the screen last actually changed, as epoch ms. Null before the first frame. */
	lastFrameAt: number | null;
	/** The device's own framebuffer size, for aspect ratio and hit-testing. */
	size: { width: number; height: number } | null;
};

const PAUSED: SimStreamStatus = { state: "paused", message: "", lastFrameAt: null, size: null };

/**
 * usePageActive reports whether this window is actually being looked at.
 *
 * Losing focus counts as not looking. That is deliberate and it has a cost — a
 * human who alt-tabs to Simulator.app sees the view freeze — so the panel says
 * so in words rather than leaving a stale frame to be mistaken for a live one.
 */
export function usePageActive(): boolean {
	return useSyncExternalStore(
		(onChange) => {
			const events: [string, EventTarget][] = [
				["visibilitychange", document],
				["focus", window],
				["blur", window],
			];
			for (const [event, target] of events) target.addEventListener(event, onChange);
			return () => {
				for (const [event, target] of events) target.removeEventListener(event, onChange);
			};
		},
		() => document.visibilityState === "visible" && document.hasFocus(),
		() => false,
	);
}

export function useSimulatorStream({
	udid,
	active,
	canvasRef,
}: {
	/** The device to watch. Null means none is chosen, so nothing connects. */
	udid: string | null;
	/** Whether somebody is looking. False closes the socket immediately. */
	active: boolean;
	canvasRef: React.RefObject<HTMLCanvasElement | null>;
}): SimStreamStatus {
	const [status, setStatus] = useState<SimStreamStatus>(PAUSED);
	const baseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	// Kept in a ref so a redraw never re-runs the connection effect.
	const drawing = useRef(false);
	const queued = useRef<ArrayBuffer | null>(null);

	useEffect(() => {
		if (!udid || !active || !baseUrl) {
			setStatus(PAUSED);
			return;
		}
		let closed = false;
		setStatus({ state: "connecting", message: "", lastFrameAt: null, size: null });

		const socket = new WebSocket(`${baseUrl.replace(/^http/, "ws")}/sim-stream/${encodeURIComponent(udid)}`);
		socket.binaryType = "arraybuffer";

		// Decoding is async, and a frame that arrives mid-decode makes the one
		// being decoded stale. Keeping only the newest is the same drop-not-queue
		// rule the daemon applies, for the same reason: on a live view the latest
		// frame is the only one worth having.
		const drawNext = async () => {
			if (drawing.current) return;
			const next = queued.current;
			queued.current = null;
			if (!next) return;
			drawing.current = true;
			try {
				const bitmap = await createImageBitmap(new Blob([next], { type: "image/jpeg" }));
				const canvas = canvasRef.current;
				if (canvas && !closed) {
					canvas.width = bitmap.width;
					canvas.height = bitmap.height;
					canvas.getContext("2d")?.drawImage(bitmap, 0, 0);
					setStatus((prev) => ({
						state: "live",
						message: "",
						lastFrameAt: Date.now(),
						size:
							prev.size?.width === bitmap.width && prev.size.height === bitmap.height
								? prev.size
								: { width: bitmap.width, height: bitmap.height },
					}));
				}
				bitmap.close();
			} catch {
				// A frame that will not decode is one lost frame, not a dead
				// stream: the next one redraws over it.
			} finally {
				drawing.current = false;
				if (queued.current) void drawNext();
			}
		};

		socket.onmessage = (event: MessageEvent) => {
			if (typeof event.data === "string") {
				let parsed: { type?: string; message?: string } = {};
				try {
					parsed = JSON.parse(event.data);
				} catch {
					parsed = { message: event.data };
				}
				setStatus((prev) => ({ ...prev, state: "ended", message: parsed.message ?? "The stream ended." }));
				return;
			}
			queued.current = event.data as ArrayBuffer;
			void drawNext();
		};
		socket.onerror = () => {
			if (closed) return;
			setStatus((prev) =>
				prev.state === "ended" ? prev : { ...prev, state: "ended", message: "Lost the connection to the daemon." },
			);
		};
		socket.onclose = () => {
			if (closed) return;
			setStatus((prev) => (prev.state === "ended" ? prev : { ...prev, state: "ended", message: "" }));
		};

		return () => {
			closed = true;
			queued.current = null;
			// Closing here is the whole CPU story: it is what tells the daemon its
			// last viewer went away, which is what stops the capture process.
			socket.close();
		};
	}, [udid, active, baseUrl, canvasRef]);

	return status;
}
