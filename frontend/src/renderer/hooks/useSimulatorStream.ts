import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { getApiBaseUrl, subscribeApiBaseUrl } from "../lib/api-client";

/**
 * The live iOS Simulator screen, drawn into a canvas.
 *
 * The socket IS the subscription. The daemon starts a capture process for a
 * device when its first viewer connects and kills it when the last one leaves,
 * so this hook's only real job is to be honest about when somebody CAN SEE the
 * screen: it connects when the Device tab is the one on screen AND the page is
 * visible, and closes the moment either stops being true. There is no timer and
 * no heartbeat to forget — closing the socket is what stops the capture.
 *
 * Focus is deliberately NOT part of that. A live view exists to be watched
 * while you are doing something else — reading the diff, typing in a terminal,
 * driving the device from Xcode — and a view that only updates while you stare
 * at it is a screenshot that refreshes when observed. The first version of this
 * hook closed the socket on blur because watching an idle screen cost 14.1% of
 * a core in the capture child; measured again on the H.264 pipeline that
 * replaced it, the same watch costs 2.1% in the capture child, 0.6% in the
 * daemon and about 4% in this app - so the cost that rule was written to prevent
 * is gone. What is kept is the rule that mattered: nobody able to look means
 * nothing running.
 *
 * Blur does still do one thing, in the other direction: coming back to the
 * window retries a stream that died while nobody was there to see it fail.
 *
 * The screen arrives as H.264 and is decoded by WebCodecs. That is what the
 * reference implementation this addon comes from does, and measuring both on
 * one device under one activity says why: the same 54 frames a second cost
 * 0.63 MB/s here against 15.6 MB/s as JPEG-per-frame, for less than half the
 * CPU. Nothing is fetched as a subresource — the bytes come over the socket the
 * page already opened — so the app:// CSP is not involved at all.
 *
 * Two consequences of H.264 that this file exists to handle:
 *
 *  - frames are not independent. A delta is meaningless without the keyframe
 *    before it, so the daemon sends a viewer nothing until it has an avcC
 *    description and a keyframe, and each message says which of the three it
 *    is. This hook trusts that ordering but does not require it: a chunk that
 *    arrives before the decoder is configured is dropped rather than decoded.
 *  - a moving screen produces frames far faster than a person reads a status
 *    line. Telling React about every frame would re-render the pane sixty times
 *    a second to move a label, so the frame clock is sampled once a second and
 *    the picture itself is drawn outside React entirely.
 */

export type SimStreamState =
	/** Nobody can see the screen, so nothing is running. */
	| "paused"
	| "connecting"
	| "live"
	/** The stream ended on its own — the device went away, or capture failed. */
	| "ended"
	/** This build cannot decode the stream at all. */
	| "unsupported";

export type SimStreamStatus = {
	state: SimStreamState;
	/** Why the stream ended, or why it cannot start. Empty while healthy. */
	message: string;
	/** When a frame last arrived, as epoch ms, sampled at most once a second. */
	lastFrameAt: number | null;
	/** The device's own framebuffer size, for aspect ratio and hit-testing. */
	size: { width: number; height: number } | null;
};

const PAUSED: SimStreamStatus = { state: "paused", message: "", lastFrameAt: null, size: null };

/** The header the daemon puts in front of every frame: kind, width, height. */
const HEADER_BYTES = 5;
const KIND_DESCRIPTION = 1;
const KIND_KEYFRAME = 2;
const KIND_DELTA = 3;

/** Frame clock for the decoder, in microseconds. Only its ordering matters. */
const FRAME_INTERVAL_US = 16_666;

/** How often the freshness clock is allowed to re-render the pane. */
const FRESHNESS_SAMPLE_MS = 1_000;

/**
 * usePageVisible reports whether this window's pixels can be seen at all.
 *
 * This is the browser's own answer rather than a guess about attention, and on
 * macOS it is the window's occlusion state: measured against a real Electron
 * window, minimised, hidden with Cmd+H and covered outright all report hidden,
 * while a window that has merely lost focus reports visible. That is the line
 * the capture is gated on, because it is the line between "nobody can see this"
 * and "somebody is watching it while doing something else".
 */
export function usePageVisible(): boolean {
	return useSyncExternalStore(
		(onChange) => {
			document.addEventListener("visibilitychange", onChange);
			return () => document.removeEventListener("visibilitychange", onChange);
		},
		() => document.visibilityState === "visible",
		() => false,
	);
}

/**
 * codecFor reads the profile, constraints and level out of an avcC blob and
 * builds the codec string WebCodecs wants. Hard-coding one would be a guess
 * about a device's screen; the encoder already said which it used.
 */
function codecFor(description: Uint8Array): string {
	const hex = [...description.subarray(1, 4)].map((b) => b.toString(16).padStart(2, "0")).join("");
	return `avc1.${hex}`;
}

export function useSimulatorStream({
	udid,
	active,
	canvasRef,
}: {
	/** The device to watch. Null means none is chosen, so nothing connects. */
	udid: string | null;
	/** Whether the screen can be seen. False closes the socket immediately. */
	active: boolean;
	canvasRef: React.RefObject<HTMLCanvasElement | null>;
}): SimStreamStatus {
	const [status, setStatus] = useState<SimStreamStatus>(PAUSED);
	const baseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	// Bumped to ask for a fresh socket. Nothing else re-runs the effect when the
	// device, the gate and the daemon address have all stayed the same.
	const [attempt, setAttempt] = useState(0);
	// A device's framebuffer size belongs to the device, not to the connection.
	// Throwing it away every time the socket is rebuilt - which happens whenever
	// the window is hidden and shown again - made the pane unable to turn a click
	// into a coordinate for the first few hundred milliseconds after a human
	// came back to the app, which is exactly when they click.
	const known = useRef<{ udid: string; size: { width: number; height: number } } | null>(null);

	useEffect(() => {
		const remembered = known.current?.udid === udid ? known.current.size : null;
		if (!udid || !active || !baseUrl) {
			setStatus({ ...PAUSED, size: remembered });
			return;
		}
		if (typeof VideoDecoder === "undefined") {
			setStatus({
				state: "unsupported",
				message: "This build cannot decode the simulator's video stream.",
				lastFrameAt: null,
				size: remembered,
			});
			return;
		}
		let closed = false;
		// The size is carried across: the last frame is still on the canvas and
		// the device has not changed shape because a socket was rebuilt.
		setStatus({ state: "connecting", message: "", lastFrameAt: null, size: remembered });

		// A decoder exists exactly while it is usable: it is created by the first
		// parameter set and dropped again if that set will not configure it. One
		// variable rather than a decoder plus a "configured" flag, because two
		// fields for one fact means a mutation can break either and the other
		// still refuses - which is how a guard survives its own tests.
		let decoder: VideoDecoder | null = null;
		let timestamp = 0;
		let reportedAt = 0;

		const fail = (message: string) => {
			if (closed) return;
			setStatus((prev) => (prev.state === "ended" ? prev : { ...prev, state: "ended", message }));
		};

		const paint = (frame: VideoFrame) => {
			try {
				const canvas = canvasRef.current;
				if (canvas && !closed) {
					if (canvas.width !== frame.displayWidth || canvas.height !== frame.displayHeight) {
						canvas.width = frame.displayWidth;
						canvas.height = frame.displayHeight;
					}
					canvas.getContext("2d")?.drawImage(frame, 0, 0);
				}
			} finally {
				// A VideoFrame holds a GPU buffer until it is closed, and the decoder
				// stalls once its pool is exhausted. This is the one line that must
				// run for every frame, painted or not.
				frame.close();
			}
			const now = Date.now();
			if (now - reportedAt < FRESHNESS_SAMPLE_MS) return;
			reportedAt = now;
			setStatus((prev) => ({ ...prev, state: "live", message: "", lastFrameAt: now }));
		};

		const configure = (description: Uint8Array) => {
			// A fresh picture group can carry a different parameter set, so the
			// decoder is reconfigured rather than assumed to still match.
			const next =
				decoder ??
				new VideoDecoder({
					output: paint,
					error: (err) => fail(err.message || "The simulator video stream could not be decoded."),
				});
			try {
				next.configure({ codec: codecFor(description), description, optimizeForLatency: true });
				decoder = next;
			} catch (err) {
				// A parameter set this build will not take is the end of the stream,
				// not one bad frame: everything after it is encoded against it.
				decoder = null;
				if (next.state !== "closed") next.close();
				fail(err instanceof Error ? err.message : "The simulator video stream could not be decoded.");
			}
		};

		const socket = new WebSocket(`${baseUrl.replace(/^http/, "ws")}/sim-stream/${encodeURIComponent(udid)}`);
		socket.binaryType = "arraybuffer";

		socket.onmessage = (event: MessageEvent) => {
			if (typeof event.data === "string") {
				let parsed: { type?: string; message?: string } = {};
				try {
					parsed = JSON.parse(event.data);
				} catch {
					parsed = { message: event.data };
				}
				fail(parsed.message ?? "The stream ended.");
				return;
			}
			const buffer = event.data as ArrayBuffer;
			if (buffer.byteLength <= HEADER_BYTES) return;
			const view = new DataView(buffer);
			const kind = view.getUint8(0);
			const width = view.getUint16(1);
			const height = view.getUint16(3);
			const payload = new Uint8Array(buffer, HEADER_BYTES);

			if (width > 0 && height > 0) {
				known.current = { udid, size: { width, height } };
				setStatus((prev) =>
					prev.size?.width === width && prev.size.height === height ? prev : { ...prev, size: { width, height } },
				);
			}

			if (kind === KIND_DESCRIPTION) {
				configure(payload);
				return;
			}
			// A kind this build does not know is not something to guess at.
			if (kind !== KIND_KEYFRAME && kind !== KIND_DELTA) return;
			// Anything before the decoder is configured cannot be decoded, and
			// feeding it in would fail the whole stream rather than one frame.
			if (!decoder) return;
			try {
				decoder.decode(
					new EncodedVideoChunk({
						type: kind === KIND_KEYFRAME ? "key" : "delta",
						timestamp: (timestamp += FRAME_INTERVAL_US),
						data: payload,
					}),
				);
			} catch {
				// One chunk the decoder refused is one lost frame; the next keyframe
				// recovers, and the daemon resynchronizes a viewer that loses frames.
			}
		};
		socket.onerror = () => fail("Lost the connection to the daemon.");
		socket.onclose = () => {
			if (closed) return;
			setStatus((prev) => (prev.state === "ended" ? prev : { ...prev, state: "ended", message: "" }));
		};

		return () => {
			closed = true;
			// Closing here is the whole CPU story: it is what tells the daemon its
			// last viewer went away, which is what stops the capture process.
			socket.close();
			if (decoder && decoder.state !== "closed") decoder.close();
			decoder = null;
		};
	}, [udid, active, attempt, baseUrl, canvasRef]);

	// Coming back to the window is what retries a stream that has ended.
	//
	// Blur no longer closes the socket, so it no longer rebuilds one either, and
	// a stream that died while the human was away - the device rebooted, the
	// daemon restarted, the capture process failed - would otherwise stay dead
	// until they switched tabs and back. Retrying when they return is the whole
	// recovery path: no timer, no button, and nothing at all while the stream is
	// healthy, so an ordinary click into the window cannot interrupt the picture.
	//
	// Focus is the only trigger it needs. Becoming visible again already rebuilds
	// the socket through the gate above, and a stream cannot end while the window
	// is out of sight - there is nothing connected to end.
	const ended = status.state === "ended";
	useEffect(() => {
		if (!ended) return;
		const retry = () => setAttempt((n) => n + 1);
		window.addEventListener("focus", retry);
		return () => window.removeEventListener("focus", retry);
	}, [ended]);

	return status;
}
