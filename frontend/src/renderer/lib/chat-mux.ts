import type { ChatEntry } from "./chat-event";
import type { MuxConnectionState } from "./terminal-mux";

type ServerFrame = {
	ch: string;
	id?: string;
	type: string;
	entry?: ChatEntry;
	delta?: unknown;
	error?: string;
};

export type ChatMuxListeners = {
	entry: (entry: ChatEntry) => void;
	delta: (delta: unknown) => void;
	/** A tailed replay sent its newest turns; the older entries follow, oldest first. */
	history: () => void;
	live: () => void;
	offline: () => void;
	error: (message: string) => void;
};

export type ChatMux = {
	open: (id: string, fromSeq: number) => void;
	close: (id: string) => void;
	listen: (id: string, listeners: ChatMuxListeners) => () => void;
	onConnectionChange: (listener: (state: MuxConnectionState) => void) => () => void;
	dispose: () => void;
};

const PING_INTERVAL_MS = 20_000;

export function createChatMux(url: string, WebSocketImpl: typeof WebSocket = WebSocket): ChatMux {
	const socket = new WebSocketImpl(url);
	const queue: string[] = [];
	const sessions = new Map<string, Set<ChatMuxListeners>>();
	const connectionListeners = new Set<(state: MuxConnectionState) => void>();
	let connectionState: MuxConnectionState | undefined;
	let pingTimer: ReturnType<typeof setInterval> | undefined;
	let disposed = false;

	const setConnectionState = (next: MuxConnectionState) => {
		if (disposed || connectionState === next) return;
		connectionState = next;
		connectionListeners.forEach((listener) => listener(next));
	};

	const send = (frame: object) => {
		if (disposed) return;
		const text = JSON.stringify(frame);
		if (socket.readyState === WebSocketImpl.OPEN) socket.send(text);
		else queue.push(text);
	};

	socket.addEventListener("open", () => {
		if (disposed) return;
		for (const frame of queue.splice(0)) socket.send(frame);
		pingTimer = setInterval(() => send({ ch: "system", type: "ping" }), PING_INTERVAL_MS);
		setConnectionState("open");
	});
	socket.addEventListener("close", () => setConnectionState("closed"));
	socket.addEventListener("error", () => setConnectionState("closed"));

	socket.addEventListener("message", (event: MessageEvent) => {
		if (typeof event.data !== "string") return;
		let frame: ServerFrame;
		try {
			frame = JSON.parse(event.data) as ServerFrame;
		} catch {
			return;
		}
		if (frame.ch !== "chat") return;
		if (frame.type === "error" && frame.id === undefined) {
			const message = frame.error ?? "unknown chat error";
			sessions.forEach((set) => set.forEach((l) => l.error(message)));
			return;
		}
		const targets = frame.id === undefined ? undefined : sessions.get(frame.id);
		if (!targets) return;
		for (const l of [...targets]) {
			switch (frame.type) {
				case "entry":
					if (frame.entry) l.entry(frame.entry);
					break;
				case "delta":
					if (frame.delta !== undefined) l.delta(frame.delta);
					break;
				case "history":
					l.history();
					break;
				case "live":
					l.live();
					break;
				case "offline":
					l.offline();
					break;
				case "error":
					l.error(frame.error ?? "unknown chat error");
					break;
			}
		}
	});

	return {
		open: (id, fromSeq) => send({ ch: "chat", type: "open", id, fromSeq, tail: true }),
		close: (id) => send({ ch: "chat", type: "close", id }),
		listen: (id, listeners) => {
			const set = sessions.get(id) ?? new Set<ChatMuxListeners>();
			set.add(listeners);
			sessions.set(id, set);
			return () => set.delete(listeners);
		},
		onConnectionChange: (listener) => {
			connectionListeners.add(listener);
			return () => connectionListeners.delete(listener);
		},
		dispose: () => {
			if (disposed) return;
			disposed = true;
			if (pingTimer) clearInterval(pingTimer);
			sessions.clear();
			connectionListeners.clear();
			try {
				socket.close();
			} catch {}
		},
	};
}
