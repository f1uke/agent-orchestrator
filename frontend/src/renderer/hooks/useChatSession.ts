// The live binding between a chat view and a chat session's event log on the
// mux `chat` channel: replay, then the host's live stream, folded into a
// transcript. It reconnects the way useTerminalSession does (500 ms doubling
// to 8 s, at once when the daemon comes back), and every reopen resumes after
// the last seq it holds, so nothing is folded twice.

import { useCallback, useEffect, useRef, useState } from "react";
import { getApiBaseUrl } from "../lib/api-client";
import type { ChatEntry } from "../lib/chat-event";
import { ChatFolder, type Transcript } from "../lib/chat-fold";
import { type ChatMux, createChatMux } from "../lib/chat-mux";
import { applyDelta, emptyOverlay, settleOverlay, type StreamOverlay } from "../lib/chat-stream";
import { muxUrlFromApiBase } from "../lib/terminal-mux";

export type ChatConnection =
	| "connecting" // first open in flight, or replaying
	| "live" // the host stream is attached
	| "offline" // the log is replayed and no host is running; retried while the session is live
	| "reconnecting" // waiting on backoff or on the daemon
	| "error"; // the server refused the stream; no automatic retry

export type ChatSessionState = {
	transcript: Transcript;
	overlay: StreamOverlay;
	/** Every log entry received, in seq order, for the Events view. */
	entries: readonly ChatEntry[];
	connection: ChatConnection;
	error?: string;
};

export type UseChatSessionOptions = {
	daemonReady: boolean;
	/** Whether a host may still appear: an offline stream of a live session is retried. */
	sessionLive: boolean;
	/** Test seam: build the mux client. Defaults to a fresh socket against the current API base. */
	createMux?: () => ChatMux;
};

const RETRY_BASE_MS = 500;
const RETRY_MAX_MS = 8_000;

function defaultCreateMux(): ChatMux {
	return createChatMux(muxUrlFromApiBase(getApiBaseUrl()));
}

type Snapshot = Pick<ChatSessionState, "transcript" | "overlay" | "entries">;

function emptySnapshot(): Snapshot {
	return { transcript: new ChatFolder().transcript(), overlay: emptyOverlay, entries: [] };
}

export function useChatSession(sessionId: string | undefined, options: UseChatSessionOptions): ChatSessionState {
	const [snapshot, setSnapshot] = useState<Snapshot>(emptySnapshot);
	const [connection, setConnection] = useState<ChatConnection>("connecting");
	const [error, setError] = useState<string | undefined>(undefined);
	const optionsRef = useRef(options);
	optionsRef.current = options;
	const connectionRef = useRef<ChatConnection>("connecting");

	const r = useRef({
		folder: new ChatFolder(),
		entries: [] as ChatEntry[],
		overlay: emptyOverlay,
		lastSeq: 0,
		mux: null as ChatMux | null,
		stopListening: [] as Array<() => void>,
		generation: 0,
		attempts: 0,
		retryTimer: null as ReturnType<typeof setTimeout> | null,
		frame: null as number | null,
	}).current;

	const transition = useCallback((next: ChatConnection, message?: string) => {
		connectionRef.current = next;
		setConnection(next);
		setError(message);
	}, []);

	// Many frames arrive per paint while a log replays; fold them all, render once.
	const scheduleFlush = useCallback(() => {
		if (r.frame !== null) return;
		r.frame = requestAnimationFrame(() => {
			r.frame = null;
			setSnapshot({ transcript: r.folder.transcript(), overlay: r.overlay, entries: [...r.entries] });
		});
	}, [r]);

	const teardown = useCallback(
		(id: string | undefined) => {
			if (r.retryTimer) clearTimeout(r.retryTimer);
			r.retryTimer = null;
			r.stopListening.forEach((stop) => stop());
			r.stopListening = [];
			if (r.mux && id) r.mux.close(id);
			r.mux?.dispose();
			r.mux = null;
		},
		[r],
	);

	const connectRef = useRef<() => void>(() => undefined);

	const scheduleRetry = useCallback(
		(show: "reconnecting" | "offline") => {
			transition(show);
			if (!optionsRef.current.daemonReady || r.retryTimer) return;
			const delay = Math.min(RETRY_BASE_MS * 2 ** r.attempts, RETRY_MAX_MS);
			r.attempts += 1;
			r.retryTimer = setTimeout(() => {
				r.retryTimer = null;
				connectRef.current();
			}, delay);
		},
		[r, transition],
	);

	const connect = useCallback(() => {
		if (!sessionId) return;
		teardown(sessionId);
		const generation = ++r.generation;
		const mux = (optionsRef.current.createMux ?? defaultCreateMux)();
		r.mux = mux;
		const current = () => r.generation === generation;
		r.stopListening.push(
			mux.listen(sessionId, {
				entry: (entry) => {
					if (!current() || entry.seq <= r.lastSeq) return;
					r.lastSeq = entry.seq;
					r.entries.push(entry);
					try {
						r.folder.apply(entry);
					} catch (err) {
						console.warn("chat: skipped an entry the fold rejected", err);
					}
					r.overlay = settleOverlay(r.overlay, entry);
					scheduleFlush();
				},
				delta: (delta) => {
					if (!current()) return;
					r.overlay = applyDelta(r.overlay, delta);
					scheduleFlush();
				},
				live: () => {
					if (!current()) return;
					r.attempts = 0;
					transition("live");
				},
				offline: () => {
					if (!current()) return;
					r.overlay = emptyOverlay;
					scheduleFlush();
					if (optionsRef.current.sessionLive) scheduleRetry("offline");
					else transition("offline");
				},
				error: (message) => {
					if (!current()) return;
					teardown(undefined);
					transition("error", message);
				},
			}),
			mux.onConnectionChange((state) => {
				if (!current() || state !== "closed") return;
				if (connectionRef.current === "error" || connectionRef.current === "offline") return;
				r.overlay = emptyOverlay;
				scheduleFlush();
				scheduleRetry("reconnecting");
			}),
		);
		mux.open(sessionId, r.lastSeq);
	}, [r, scheduleFlush, scheduleRetry, sessionId, teardown, transition]);
	connectRef.current = connect;

	useEffect(() => {
		r.folder = new ChatFolder();
		r.entries = [];
		r.overlay = emptyOverlay;
		r.lastSeq = 0;
		r.attempts = 0;
		setSnapshot(emptySnapshot());
		if (!sessionId) return;
		if (optionsRef.current.daemonReady) {
			transition("connecting");
			connect();
		} else {
			transition("reconnecting");
		}
		return () => {
			r.generation += 1;
			teardown(sessionId);
			if (r.frame !== null) cancelAnimationFrame(r.frame);
			r.frame = null;
		};
	}, [connect, r, sessionId, teardown, transition]);

	// The daemon came back while we waited, or the session came back to life
	// while its stream sat offline: reconnect now, without backoff debt.
	const { daemonReady, sessionLive } = options;
	useEffect(() => {
		if (!daemonReady || !sessionId || r.retryTimer) return;
		const state = connectionRef.current;
		if (state !== "reconnecting" && !(state === "offline" && sessionLive)) return;
		r.attempts = 0;
		connect();
	}, [connect, daemonReady, r, sessionId, sessionLive]);

	return { ...snapshot, connection, error };
}
