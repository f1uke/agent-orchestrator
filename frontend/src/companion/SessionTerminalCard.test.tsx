import { render, fireEvent, cleanup } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SessionTerminalCard } from "./SessionTerminalCard";
import { STATUS_LABELS } from "./preview";

// The card is xterm glue, so xterm itself is faked: what these tests are about is
// everything AROUND the terminal — what the card says it is, how it closes, and
// the detach the hand-off depends on.

const state = vi.hoisted(() => ({
	keyListeners: new Set<(event: { key: string }) => void>(),
	focused: 0,
	disposed: 0,
}));

vi.mock("@xterm/xterm", () => ({
	Terminal: class FakeTerminal {
		cols = 80;
		rows = 24;
		textarea = document.createElement("textarea");
		options = {};
		loadAddon() {}
		open() {}
		focus() {
			state.focused += 1;
		}
		write() {}
		writeln() {}
		clear() {}
		dispose() {
			state.disposed += 1;
		}
		onKey(listener: (event: { key: string }) => void) {
			state.keyListeners.add(listener);
			return { dispose: () => state.keyListeners.delete(listener) };
		}
		onResize() {
			return { dispose: () => undefined };
		}
	},
}));
vi.mock("@xterm/addon-fit", () => ({
	FitAddon: class {
		fit() {}
	},
}));
vi.mock("@xterm/xterm/css/xterm.css", () => ({}));

const attach = vi.fn();
const detach = vi.fn();
vi.mock("../renderer/hooks/useTerminalSession", () => ({
	useTerminalSession: () => ({
		attach: (...args: unknown[]) => {
			attach(...args);
			return detach;
		},
		state: "attached",
		error: undefined,
	}),
}));

afterEach(() => {
	cleanup();
	state.keyListeners.clear();
	state.focused = 0;
	state.disposed = 0;
	attach.mockClear();
	detach.mockClear();
});

const card = (overrides: Partial<Parameters<typeof SessionTerminalCard>[0]> = {}) =>
	render(
		<SessionTerminalCard
			handleId="pane-1"
			title="fix the flaky test"
			daemonUrl="http://127.0.0.1:3987"
			onClose={() => undefined}
			{...overrides}
		/>,
	);

describe("the card a Proc opens", () => {
	it("says which session it is, in the words the board uses", () => {
		const { getByText } = card();

		expect(getByText("fix the flaky test")).toBeTruthy();
	});

	it("marks a session that wants you differently from one getting on with it", () => {
		// The overlay draws ONE distinction, the same one its speech bubbles draw.
		const waiting = card({ status: "needs_input" }).container.querySelector("[title]");
		cleanup();
		const working = card({ status: "working" }).container.querySelector("[title]");

		expect(waiting?.getAttribute("title")).toBe(STATUS_LABELS.needs_input);
		expect(working?.getAttribute("title")).toBe(STATUS_LABELS.working);
		expect((waiting as HTMLElement).style.background).not.toBe((working as HTMLElement).style.background);
	});

	it("points at its Proc, whichever side of the card it is on", () => {
		const centre = card().container.querySelector("[data-terminal-tail]");
		expect(centre).not.toBeNull();
		cleanup();

		const left = card({ tail: "left" }).container.querySelector<HTMLElement>("[data-terminal-tail]");
		expect(left?.style.left).toBe("28px");
		cleanup();

		const right = card({ tail: "right" }).container.querySelector<HTMLElement>("[data-terminal-tail]");
		expect(right?.style.right).toBe("28px");
	});

	it("closes on the ✕ and on Escape, because the window is not the page's to destroy", () => {
		const onClose = vi.fn();
		const { getByLabelText } = card({ onClose });

		fireEvent.click(getByLabelText("Close terminal"));
		expect(onClose).toHaveBeenCalledTimes(1);

		fireEvent.keyDown(window, { key: "Escape" });
		expect(onClose).toHaveBeenCalledTimes(2);
	});

	it("takes the pointer back from the overlay's click-through stylesheet", () => {
		// The window shares companion.css, which sets `pointer-events: none` on
		// html/body so the BAND is a hole in the screen. Inheriting that here made
		// every click — the ✕ included — pass straight through the card to the page
		// root: the button never fired and the stray click blurred the terminal, so
		// typing died. The card must reclaim the pointer for itself.
		const { container } = card();
		const root = container.querySelector<HTMLElement>("[data-session-terminal]");

		expect(root?.style.pointerEvents).toBe("auto");
	});

	it("does not let a click on the header steal the caret from the terminal", () => {
		// A toolbar over a terminal must not take focus: clicking the title or the
		// status text would otherwise leave you unable to type until you clicked back
		// into the terminal. Preventing the mousedown's default keeps focus put — and
		// it does not cancel the click, so the ✕ still closes.
		const onClose = vi.fn();
		const { getByText, getByLabelText } = card({ title: "fix the flaky test", onClose });

		const header = getByText("fix the flaky test").parentElement!;
		const onHeader = new MouseEvent("mousedown", { bubbles: true, cancelable: true });
		header.dispatchEvent(onHeader);
		expect(onHeader.defaultPrevented).toBe(true);

		// The ✕ lives in that same header, so its mousedown is prevented too — which
		// must NOT stop its click from closing.
		fireEvent.click(getByLabelText("Close terminal"));
		expect(onClose).toHaveBeenCalled();
	});

	it("hands its detach up, and takes it back when it goes", () => {
		// The hand-off calls this before the window is destroyed; a destroyed window's
		// cleanup never runs, so the pane would otherwise never be told to let go.
		const registered: Array<(() => void) | null> = [];
		const view = card({ registerDetach: (fn) => registered.push(fn) });

		expect(registered.filter(Boolean)).toHaveLength(1);
		view.unmount();
		expect(registered.at(-1)).toBeNull();
		expect(detach).toHaveBeenCalled();
	});

	it("attaches the terminal it built, and puts the caret in it", () => {
		card();

		expect(attach).toHaveBeenCalledTimes(1);
		expect(state.focused).toBeGreaterThan(0);
	});

	it("reports that it is being used when the human types", () => {
		// This is what keeps a terminal somebody is working in from closing itself.
		const onActivity = vi.fn();
		card({ onActivity });

		state.keyListeners.forEach((listener) => listener({ key: "a" }));

		expect(onActivity).toHaveBeenCalled();
	});
});
