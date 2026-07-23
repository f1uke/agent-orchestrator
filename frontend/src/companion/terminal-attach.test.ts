import { describe, expect, it, vi } from "vitest";
import { attachBubbleTerminal, type BubbleTerminal, type TerminalMux } from "./terminal-attach";

// PROTOTYPE (terminal bubble). The tests that matter here are about DETACH, not
// attach: the hand-off to the board window is only safe if letting go of a bubble
// closes the pane, closes the socket, and stops sending — in that order.

function fakeMux() {
	const calls: string[] = [];
	const listeners = {
		data: new Map<string, (bytes: Uint8Array) => void>(),
		opened: new Map<string, () => void>(),
		exit: new Map<string, () => void>(),
		error: new Map<string, (message: string) => void>(),
	};
	const mux: TerminalMux & { calls: string[]; listeners: typeof listeners } = {
		calls,
		listeners,
		open: (id, cols, rows) => calls.push(`open:${id}:${cols}x${rows}`),
		sendInput: (id, input) => calls.push(`input:${id}:${input}`),
		resize: (id, cols, rows) => calls.push(`resize:${id}:${cols}x${rows}`),
		close: (id) => calls.push(`close:${id}`),
		onData: (id, listener) => {
			listeners.data.set(id, listener);
			return () => listeners.data.delete(id);
		},
		onExit: (id, listener) => {
			listeners.exit.set(id, listener);
			return () => listeners.exit.delete(id);
		},
		onOpened: (id, listener) => {
			listeners.opened.set(id, listener);
			return () => listeners.opened.delete(id);
		},
		onError: (id, listener) => {
			listeners.error.set(id, listener);
			return () => listeners.error.delete(id);
		},
		onConnectionChange: () => () => undefined,
		dispose: () => calls.push("dispose"),
	};
	return mux;
}

function fakeTerminal() {
	const written: string[] = [];
	let input: ((data: string) => void) | null = null;
	let resize: ((size: { cols: number; rows: number }) => void) | null = null;
	const terminal: BubbleTerminal & {
		written: string[];
		type(data: string): void;
		resizeTo(cols: number, rows: number): void;
		inputBound(): boolean;
	} = {
		cols: 80,
		rows: 24,
		written,
		write: (bytes) => written.push(new TextDecoder().decode(bytes)),
		onInput: (listener) => {
			input = listener;
			return {
				dispose: () => {
					input = null;
				},
			};
		},
		onResize: (listener) => {
			resize = listener;
			return {
				dispose: () => {
					resize = null;
				},
			};
		},
		type: (data) => input?.(data),
		resizeTo: (cols, rows) => resize?.({ cols, rows }),
		inputBound: () => input !== null,
	};
	return terminal;
}

const setup = () => {
	const mux = fakeMux();
	const terminal = fakeTerminal();
	const states: string[] = [];
	const detach = attachBubbleTerminal({
		terminal,
		handleId: "pane-1",
		muxUrl: "ws://127.0.0.1:3987/mux",
		onState: (state) => states.push(state),
		createMux: () => mux,
	});
	return { mux, terminal, states, detach };
};

describe("attaching a bubble terminal", () => {
	it("opens the pane at the grid the terminal already has", () => {
		const { mux } = setup();

		expect(mux.calls).toEqual(["open:pane-1:80x24", "resize:pane-1:80x24"]);
	});

	it("writes the session's output into the terminal", () => {
		const { mux, terminal } = setup();

		mux.listeners.data.get("pane-1")?.(new TextEncoder().encode("hello from tmux"));

		expect(terminal.written).toEqual(["hello from tmux"]);
	});

	it("sends what the human types to the pane", () => {
		const { mux, terminal } = setup();

		terminal.type("ls\r");

		expect(mux.calls).toContain("input:pane-1:ls\r");
	});

	it("reports the states the card shows", () => {
		const { mux, states } = setup();
		mux.listeners.opened.get("pane-1")?.();
		mux.listeners.exit.get("pane-1")?.();

		expect(states).toEqual(["connecting", "attached", "exited"]);
	});
});

describe("detaching — the hand-off's safety property", () => {
	it("closes the PANE before dropping the socket, so no attach client is orphaned", () => {
		const { mux, detach } = setup();
		mux.calls.length = 0;

		detach();

		expect(mux.calls).toEqual(["close:pane-1", "dispose"]);
	});

	it("stops sending input the moment it lets go", () => {
		const { mux, terminal, detach } = setup();
		detach();
		mux.calls.length = 0;

		terminal.type("rm -rf /");

		expect(mux.calls).toEqual([]);
		expect(terminal.inputBound()).toBe(false);
	});

	it("stops writing output into a terminal that is going away", () => {
		const { mux, terminal, detach } = setup();
		detach();

		mux.listeners.data.get("pane-1")?.(new TextEncoder().encode("late frame"));

		expect(terminal.written).toEqual([]);
	});

	it("is idempotent — a second detach closes nothing twice", () => {
		const { mux, detach } = setup();
		detach();
		mux.calls.length = 0;

		detach();

		expect(mux.calls).toEqual([]);
	});

	it("drops a resize that was still in its debounce window", () => {
		vi.useFakeTimers();
		try {
			const { mux, terminal, detach } = setup();
			terminal.resizeTo(100, 40);
			detach();
			mux.calls.length = 0;
			vi.advanceTimersByTime(500);

			expect(mux.calls).toEqual([]);
		} finally {
			vi.useRealTimers();
		}
	});
});
