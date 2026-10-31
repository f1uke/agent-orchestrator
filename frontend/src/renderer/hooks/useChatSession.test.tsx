import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatEntry } from "../lib/chat-event";
import type { ChatMux, ChatMuxListeners } from "../lib/chat-mux";
import type { MuxConnectionState } from "../lib/terminal-mux";
import { useChatSession } from "./useChatSession";

type FakeMux = {
	mux: ChatMux;
	opens: Array<[string, number]>;
	disposed: boolean;
	listeners?: ChatMuxListeners;
	emitConnection(state: MuxConnectionState): void;
};

function createFakeMux(): FakeMux {
	const connection = new Set<(state: MuxConnectionState) => void>();
	const fake: FakeMux = {
		opens: [],
		disposed: false,
		mux: {
			open: (id, fromSeq) => fake.opens.push([id, fromSeq]),
			close: () => undefined,
			listen: (_id, listeners) => {
				fake.listeners = listeners;
				return () => {
					fake.listeners = undefined;
				};
			},
			onConnectionChange: (listener) => {
				connection.add(listener);
				return () => connection.delete(listener);
			},
			dispose: () => {
				fake.disposed = true;
			},
		},
		emitConnection: (state) => connection.forEach((l) => l(state)),
	};
	return fake;
}

const userInput = (seq: number, uuid: string, text: string): ChatEntry => ({
	seq,
	ts: "2026-10-30T10:00:00Z",
	dir: "in",
	payload: { type: "user", uuid, message: { content: text } },
});

const assistant = (seq: number, id: string, text: string): ChatEntry => ({
	seq,
	ts: "2026-10-30T10:00:01Z",
	dir: "out",
	payload: { type: "assistant", message: { id, content: [{ type: "text", text }] } },
});

const streamEvent = (event: object) => ({ type: "stream_event", parent_tool_use_id: null, event });

function setup({ daemonReady = true, sessionLive = true } = {}) {
	const muxes: FakeMux[] = [];
	const createMux = () => {
		const fake = createFakeMux();
		muxes.push(fake);
		return fake.mux;
	};
	const view = renderHook((props) => useChatSession("chat-1", { createMux, ...props }), {
		initialProps: { daemonReady, sessionLive },
	});
	const frame = () => act(() => void vi.advanceTimersToNextFrame());
	const send = (fn: (l: ChatMuxListeners) => void) =>
		act(() => {
			fn(muxes.at(-1)!.listeners!);
		});
	return { view, muxes, frame, send };
}

beforeEach(() => {
	vi.useFakeTimers();
});

afterEach(() => {
	vi.useRealTimers();
});

describe("useChatSession", () => {
	it("opens from seq 0 and folds replayed entries into the transcript on the next frame", () => {
		const { view, muxes, frame, send } = setup();
		expect(muxes[0].opens).toEqual([["chat-1", 0]]);
		send((l) => {
			l.entry(userInput(1, "u1", "hello"));
			l.entry(assistant(2, "m1", "hi there"));
			l.live();
		});
		frame();
		expect(view.result.current.connection).toBe("live");
		expect(view.result.current.transcript.items.map((i) => i.kind)).toEqual(["user", "assistant_text"]);
		expect(view.result.current.entries.map((e) => e.seq)).toEqual([1, 2]);
	});

	it("reopens after the last seq it holds when an offline stream belongs to a live session", () => {
		const { view, muxes, frame, send } = setup({ sessionLive: true });
		send((l) => {
			l.entry(userInput(1, "u1", "hello"));
			l.entry(assistant(2, "m1", "hi"));
			l.offline();
		});
		frame();
		expect(view.result.current.connection).toBe("offline");
		expect(muxes).toHaveLength(1);
		act(() => void vi.advanceTimersByTime(500));
		expect(muxes).toHaveLength(2);
		expect(muxes[1].opens).toEqual([["chat-1", 2]]);
		send((l) => {
			l.entry(assistant(2, "m1", "hi"));
			l.entry(assistant(3, "m2", "again"));
		});
		frame();
		expect(view.result.current.transcript.items).toHaveLength(3);
		expect(view.result.current.entries.map((e) => e.seq)).toEqual([1, 2, 3]);
	});

	it("stays offline without retrying when the session is not live", () => {
		const { view, muxes, frame, send } = setup({ sessionLive: false });
		send((l) => l.offline());
		frame();
		act(() => void vi.advanceTimersByTime(30_000));
		expect(view.result.current.connection).toBe("offline");
		expect(muxes).toHaveLength(1);
	});

	it("reconnects a dropped socket after the last seq, backing off 500 ms then 1 s", () => {
		const { view, muxes, send } = setup();
		send((l) => {
			l.entry(userInput(5, "u1", "hello"));
			l.live();
		});
		act(() => muxes[0].emitConnection("closed"));
		expect(view.result.current.connection).toBe("reconnecting");
		expect(muxes[0].disposed).toBe(false);
		act(() => void vi.advanceTimersByTime(499));
		expect(muxes).toHaveLength(1);
		act(() => void vi.advanceTimersByTime(1));
		expect(muxes[1].opens).toEqual([["chat-1", 5]]);
		expect(muxes[0].disposed).toBe(true);
		act(() => muxes[1].emitConnection("closed"));
		act(() => void vi.advanceTimersByTime(999));
		expect(muxes).toHaveLength(2);
		act(() => void vi.advanceTimersByTime(1));
		expect(muxes[2].opens).toEqual([["chat-1", 5]]);
	});

	it("waits for the daemon, then connects at once when it is ready", () => {
		const { view, muxes } = setup({ daemonReady: false });
		expect(muxes).toHaveLength(0);
		expect(view.result.current.connection).toBe("reconnecting");
		view.rerender({ daemonReady: true, sessionLive: true });
		expect(muxes).toHaveLength(1);
		expect(muxes[0].opens).toEqual([["chat-1", 0]]);
	});

	it("does not retry a stream the server refused", () => {
		const { view, muxes, send } = setup();
		send((l) => l.error("session chat-1 is not a chat session"));
		act(() => void vi.advanceTimersByTime(30_000));
		expect(view.result.current.connection).toBe("error");
		expect(view.result.current.error).toBe("session chat-1 is not a chat session");
		expect(muxes).toHaveLength(1);
	});

	it("streams deltas into the overlay until the logged assistant block replaces them", () => {
		const { view, frame, send } = setup();
		send((l) => {
			l.live();
			l.delta(streamEvent({ type: "message_start", message: { id: "m1" } }));
			l.delta(streamEvent({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "Hel" } }));
			l.delta(streamEvent({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "lo" } }));
		});
		frame();
		expect(view.result.current.overlay.blocks).toEqual([{ messageId: "m1", index: 0, kind: "text", text: "Hello" }]);
		expect(view.result.current.transcript.items).toEqual([]);
		send((l) => l.entry(assistant(1, "m1", "Hello")));
		frame();
		expect(view.result.current.overlay.blocks).toEqual([]);
		expect(view.result.current.transcript.items).toEqual([
			{ kind: "assistant_text", messageId: "m1", text: "Hello", parentToolUseId: "" },
		]);
	});

	it("draws a replay once, when it ends, not frame by frame while it streams in", () => {
		const { view, frame, send } = setup();
		send((l) => l.entry(userInput(1, "u1", "hello")));
		frame();
		expect(view.result.current.transcript.items).toEqual([]);
		expect(view.result.current.connection).toBe("connecting");
		expect(view.result.current.replaying).toBe(true);
		send((l) => {
			l.entry(assistant(2, "m1", "hi"));
			l.live();
		});
		frame();
		expect(view.result.current.connection).toBe("live");
		expect(view.result.current.transcript.items.map((i) => i.kind)).toEqual(["user", "assistant_text"]);
	});

	it("draws the newest turns at the history marker, then the whole log in order when the replay ends", () => {
		const { view, frame, send } = setup();
		send((l) => {
			l.entry(userInput(3, "u2", "second"));
			l.entry(assistant(4, "m2", "two"));
			l.history();
		});
		frame();
		expect(view.result.current.connection).toBe("connecting");
		expect(view.result.current.replaying).toBe(false);
		expect(view.result.current.transcript.items.map((i) => (i.kind === "user" ? i.text : i.kind))).toEqual([
			"second",
			"assistant_text",
		]);
		send((l) => {
			l.entry(userInput(1, "u1", "first"));
			l.entry(assistant(2, "m1", "one"));
			l.offline();
		});
		frame();
		expect(view.result.current.connection).toBe("offline");
		expect(view.result.current.transcript.items.map((i) => (i.kind === "user" ? i.text : i.kind))).toEqual([
			"first",
			"assistant_text",
			"second",
			"assistant_text",
		]);
		expect(view.result.current.entries.map((e) => e.seq)).toEqual([1, 2, 3, 4]);
		expect(view.result.current.transcript.lastSeq).toBe(4);
	});

	it("folds what the host logs after a tailed replay, as it arrives", () => {
		const { view, frame, send } = setup();
		send((l) => {
			l.entry(userInput(2, "u2", "second"));
			l.history();
			l.entry(userInput(1, "u1", "first"));
			l.live();
		});
		frame();
		send((l) => l.entry(assistant(3, "m2", "two")));
		frame();
		expect(view.result.current.transcript.items.map((i) => (i.kind === "user" ? i.text : i.kind))).toEqual([
			"first",
			"second",
			"assistant_text",
		]);
	});

	it("starts a replay that dropped before it ended over from the beginning, so no older entry goes missing", () => {
		const { view, muxes, frame, send } = setup();
		send((l) => {
			l.entry(userInput(3, "u2", "second"));
			l.history();
			l.entry(userInput(1, "u1", "first"));
		});
		act(() => muxes[0].emitConnection("closed"));
		act(() => void vi.advanceTimersByTime(500));
		expect(muxes[1].opens).toEqual([["chat-1", 0]]);
		send((l) => {
			l.entry(userInput(3, "u2", "second"));
			l.history();
			l.entry(userInput(1, "u1", "first"));
			l.entry(assistant(2, "m1", "one"));
			l.live();
		});
		frame();
		expect(view.result.current.entries.map((e) => e.seq)).toEqual([1, 2, 3]);
		expect(view.result.current.transcript.items.map((i) => (i.kind === "user" ? i.text : i.kind))).toEqual([
			"first",
			"assistant_text",
			"second",
		]);
	});

	it("never hands a new session the transcript of the session it replaced", () => {
		const muxes: FakeMux[] = [];
		const createMux = () => {
			const fake = createFakeMux();
			muxes.push(fake);
			return fake.mux;
		};
		const renders: Array<{ id: string; texts: string[] }> = [];
		const view = renderHook(
			({ id }) => {
				const state = useChatSession(id, { createMux, daemonReady: true, sessionLive: true });
				renders.push({ id, texts: state.transcript.items.map((i) => (i.kind === "user" ? i.text : i.kind)) });
				return state;
			},
			{ initialProps: { id: "chat-1" } },
		);
		act(() => {
			muxes[0].listeners!.entry(userInput(1, "u1", "from chat-1"));
			muxes[0].listeners!.live();
		});
		act(() => void vi.advanceTimersToNextFrame());
		expect(view.result.current.transcript.items).toHaveLength(1);

		renders.length = 0;
		view.rerender({ id: "chat-2" });
		expect(renders.filter((r) => r.id === "chat-2").map((r) => r.texts)).not.toContainEqual(["from chat-1"]);
	});
});
