import { afterEach, describe, expect, it } from "vitest";
import { type ChatMuxListeners, createChatMux } from "./chat-mux";

class FakeSocket {
	static OPEN = 1;
	static instances: FakeSocket[] = [];
	readyState = 0;
	sent: string[] = [];
	closed = false;
	private listeners: Record<string, ((ev: unknown) => void)[]> = {};
	constructor(public url: string) {
		FakeSocket.instances.push(this);
	}
	addEventListener(type: string, cb: (ev: unknown) => void) {
		(this.listeners[type] ??= []).push(cb);
	}
	send(frame: string) {
		this.sent.push(frame);
	}
	close() {
		this.closed = true;
	}
	emitOpen() {
		this.readyState = FakeSocket.OPEN;
		this.listeners.open?.forEach((cb) => cb({}));
	}
	emit(frame: object) {
		this.listeners.message?.forEach((cb) => cb({ data: JSON.stringify(frame) }));
	}
	emitClose() {
		this.listeners.close?.forEach((cb) => cb({}));
	}
	emitError() {
		this.listeners.error?.forEach((cb) => cb({}));
	}
}

function connect() {
	const mux = createChatMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
	const socket = FakeSocket.instances.at(-1)!;
	return { mux, socket };
}

function recorder() {
	const seen: string[] = [];
	const listeners: ChatMuxListeners = {
		entry: (e) => seen.push(`entry:${e.seq}`),
		delta: (d) => seen.push(`delta:${JSON.stringify(d)}`),
		live: () => seen.push("live"),
		offline: () => seen.push("offline"),
		error: (m) => seen.push(`error:${m}`),
	};
	return { seen, listeners };
}

afterEach(() => {
	FakeSocket.instances = [];
});

describe("createChatMux", () => {
	it("queues open until the socket connects, carrying the seq to resume after", () => {
		const { mux, socket } = connect();
		mux.open("s1", 42);
		expect(socket.sent).toEqual([]);
		socket.emitOpen();
		expect(socket.sent.map((f) => JSON.parse(f))).toEqual([{ ch: "chat", type: "open", id: "s1", fromSeq: 42 }]);
		mux.close("s1");
		expect(JSON.parse(socket.sent[1])).toEqual({ ch: "chat", type: "close", id: "s1" });
	});

	it("routes each chat frame type to its session's listener and nobody else's", () => {
		const { mux, socket } = connect();
		socket.emitOpen();
		const mine = recorder();
		const other = recorder();
		mux.listen("s1", mine.listeners);
		mux.listen("s2", other.listeners);
		socket.emit({ ch: "chat", id: "s1", type: "entry", entry: { seq: 7, ts: "", dir: "out", payload: { type: "x" } } });
		socket.emit({ ch: "chat", id: "s1", type: "delta", delta: { type: "stream_event" } });
		socket.emit({ ch: "chat", id: "s1", type: "live" });
		socket.emit({ ch: "chat", id: "s1", type: "offline" });
		socket.emit({ ch: "chat", id: "s1", type: "error", error: "unknown session s1" });
		expect(mine.seen).toEqual([
			"entry:7",
			'delta:{"type":"stream_event"}',
			"live",
			"offline",
			"error:unknown session s1",
		]);
		expect(other.seen).toEqual([]);
	});

	it("ignores frames of other channels, including a terminal frame with the same id", () => {
		const { mux, socket } = connect();
		socket.emitOpen();
		const mine = recorder();
		mux.listen("s1", mine.listeners);
		socket.emit({ ch: "terminal", id: "s1", type: "error", error: "pane gone" });
		socket.emit({ ch: "sessions", type: "snapshot" });
		expect(mine.seen).toEqual([]);
	});

	it("broadcasts an id-less chat error to every session", () => {
		const { mux, socket } = connect();
		socket.emitOpen();
		const a = recorder();
		const b = recorder();
		mux.listen("a", a.listeners);
		mux.listen("b", b.listeners);
		socket.emit({ ch: "chat", type: "error", error: "missing session id" });
		expect([a.seen, b.seen]).toEqual([["error:missing session id"], ["error:missing session id"]]);
	});

	it("stops delivering after the listener is removed or the mux is disposed", () => {
		const { mux, socket } = connect();
		socket.emitOpen();
		const mine = recorder();
		const stop = mux.listen("s1", mine.listeners);
		stop();
		socket.emit({ ch: "chat", id: "s1", type: "live" });
		const again = recorder();
		mux.listen("s1", again.listeners);
		mux.dispose();
		socket.emit({ ch: "chat", id: "s1", type: "live" });
		expect([mine.seen, again.seen, socket.closed]).toEqual([[], [], true]);
	});

	it("reports one closed transition for an error followed by close, and none for its own dispose", () => {
		const first = connect();
		const states: string[] = [];
		first.mux.onConnectionChange((s) => states.push(s));
		first.socket.emitOpen();
		first.socket.emitError();
		first.socket.emitClose();
		expect(states).toEqual(["open", "closed"]);

		const second = connect();
		second.socket.emitOpen();
		const after: string[] = [];
		second.mux.onConnectionChange((s) => after.push(s));
		second.mux.dispose();
		second.socket.emitClose();
		expect(after).toEqual([]);
	});
});
