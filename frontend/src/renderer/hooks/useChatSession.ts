import { useCallback, useEffect, useRef, useState } from "react";
import { getApiBaseUrl } from "../lib/api-client";
import type { ChatEntry } from "../lib/chat-event";
import { ChatFolder, type Transcript } from "../lib/chat-fold";
import { createStubChatMux } from "../lib/chat-mock";
import { type ChatMux, createChatMux } from "../lib/chat-mux";
import { applyDelta, emptyOverlay, settleOverlay, type StreamOverlay } from "../lib/chat-stream";
import { muxUrlFromApiBase } from "../lib/terminal-mux";

export type ChatConnection = "connecting" | "live" | "offline" | "reconnecting" | "error";

export type ChatSessionState = {
	transcript: Transcript;
	overlay: StreamOverlay;
	entries: readonly ChatEntry[];
	connection: ChatConnection;
	/** True until the snapshot holds the newest turns: the stream has nothing worth showing yet. */
	replaying: boolean;
	error?: string;
};

export type UseChatSessionOptions = {
	daemonReady: boolean;
	sessionLive: boolean;
	createMux?: () => ChatMux;
};

type Replay = "holdNewest" | "holdOlder" | "inOrder";

const RETRY_BASE_MS = 500;
const RETRY_MAX_MS = 8_000;

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

function defaultCreateMux(): ChatMux {
	if (usePreviewData) return createStubChatMux();
	return createChatMux(muxUrlFromApiBase(getApiBaseUrl()));
}

// A snapshot names the session it was folded from: on a switch, the render
// before the reset effect must show the new session as empty, not the old
// session's rows in the new session's pane.
type Snapshot = Pick<ChatSessionState, "transcript" | "overlay" | "entries" | "replaying"> & {
	sessionId: string | undefined;
};

function emptySnapshot(sessionId: string | undefined): Snapshot {
	return { sessionId, transcript: new ChatFolder().transcript(), overlay: emptyOverlay, entries: [], replaying: true };
}

export function useChatSession(sessionId: string | undefined, given: UseChatSessionOptions): ChatSessionState {
	const options = usePreviewData ? { ...given, daemonReady: true } : given;
	const [snapshot, setSnapshot] = useState<Snapshot>(() => emptySnapshot(sessionId));
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
		replay: "inOrder" as Replay,
		sessionId,
	}).current;

	const transition = useCallback((next: ChatConnection, message?: string) => {
		connectionRef.current = next;
		setConnection(next);
		setError(message);
	}, []);

	const scheduleFlush = useCallback(() => {
		if (r.frame !== null) return;
		r.frame = requestAnimationFrame(() => {
			r.frame = null;
			setSnapshot({
				sessionId: r.sessionId,
				transcript: r.folder.transcript(),
				overlay: r.overlay,
				entries: [...r.entries],
				replaying: r.replay === "holdNewest",
			});
		});
	}, [r]);

	const foldInSeqOrder = useCallback(() => {
		if (r.replay === "inOrder") return;
		r.replay = "inOrder";
		r.entries.sort((a, b) => a.seq - b.seq);
		r.folder = new ChatFolder();
		for (const entry of r.entries) {
			try {
				r.folder.apply(entry);
			} catch (err) {
				console.warn("chat: skipped an entry the fold rejected", err);
			}
		}
		scheduleFlush();
	}, [r, scheduleFlush]);

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

	const forgetLog = useCallback(() => {
		r.folder = new ChatFolder();
		r.entries = [];
		r.lastSeq = 0;
	}, [r]);

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
					if (!current()) return;
					if (r.replay === "holdOlder") {
						r.entries.push(entry);
						return;
					}
					if (entry.seq <= r.lastSeq) return;
					r.lastSeq = entry.seq;
					r.entries.push(entry);
					try {
						r.folder.apply(entry);
					} catch (err) {
						console.warn("chat: skipped an entry the fold rejected", err);
					}
					r.overlay = settleOverlay(r.overlay, entry);
					if (r.replay === "inOrder") scheduleFlush();
				},
				history: () => {
					if (!current() || r.replay !== "holdNewest") return;
					r.replay = "holdOlder";
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
					foldInSeqOrder();
					transition("live");
				},
				offline: () => {
					if (!current()) return;
					foldInSeqOrder();
					r.overlay = emptyOverlay;
					scheduleFlush();
					if (optionsRef.current.sessionLive) scheduleRetry("offline");
					else transition("offline");
				},
				error: (message) => {
					if (!current()) return;
					foldInSeqOrder();
					teardown(undefined);
					transition("error", message);
				},
			}),
			mux.onConnectionChange((state) => {
				if (!current() || state !== "closed") return;
				if (connectionRef.current === "error" || connectionRef.current === "offline") return;
				if (r.replay !== "inOrder") forgetLog();
				r.overlay = emptyOverlay;
				scheduleFlush();
				scheduleRetry("reconnecting");
			}),
		);
		r.replay = r.lastSeq === 0 ? "holdNewest" : "inOrder";
		mux.open(sessionId, r.lastSeq);
	}, [foldInSeqOrder, r, forgetLog, scheduleFlush, scheduleRetry, sessionId, teardown, transition]);
	connectRef.current = connect;

	useEffect(() => {
		forgetLog();
		r.overlay = emptyOverlay;
		r.attempts = 0;
		r.sessionId = sessionId;
		setSnapshot(emptySnapshot(sessionId));
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
	}, [connect, r, forgetLog, sessionId, teardown, transition]);

	const { daemonReady, sessionLive } = options;
	useEffect(() => {
		if (!daemonReady || !sessionId || r.retryTimer) return;
		const state = connectionRef.current;
		if (state !== "reconnecting" && !(state === "offline" && sessionLive)) return;
		r.attempts = 0;
		connect();
	}, [connect, daemonReady, r, sessionId, sessionLive]);

	const current = snapshot.sessionId === sessionId;
	const shown = current ? snapshot : emptySnapshot(sessionId);
	return {
		transcript: shown.transcript,
		overlay: shown.overlay,
		entries: shown.entries,
		replaying: shown.replaying,
		connection: current ? connection : "connecting",
		error: current ? error : undefined,
	};
}
