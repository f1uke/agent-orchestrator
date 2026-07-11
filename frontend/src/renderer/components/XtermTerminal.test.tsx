import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { XtermTerminal } from "./XtermTerminal";
import { focusTerminal } from "../lib/terminal-focus";
import { findSessionLinks } from "../lib/session-ref";
import { findExternalRefLinks } from "../lib/terminal-scm-links";

type FakeLink = {
	text: string;
	range: { start: { x: number; y: number }; end: { x: number; y: number } };
	activate: (event: MouseEvent, text: string) => void;
};
type FakeLinkProvider = { provideLinks: (line: number, callback: (links: FakeLink[] | undefined) => void) => void };

const state = vi.hoisted(() => ({
	linkHandler: null as null | ((event: MouseEvent, uri: string) => void),
	lastTerminal: null as null | {
		keyHandler?: (event: KeyboardEvent) => boolean;
		wheelHandler?: (event: WheelEvent) => boolean;
		focus: ReturnType<typeof vi.fn>;
		selection: string;
		options: Record<string, unknown>;
		modes: { bracketedPasteMode: boolean; mouseTrackingMode: string };
		lines: string[];
		buffer: { active: { type: string; getLine: (i: number) => { translateToString: () => string } | undefined } };
		linkProvider?: FakeLinkProvider;
		linkProviders: FakeLinkProvider[];
		scrollLines: ReturnType<typeof vi.fn>;
		dataListeners: Set<(data: string) => void>;
		binaryListeners: Set<(data: string) => void>;
		keyListeners: Set<(event: { key: string }) => void>;
		selectionListeners: Set<() => void>;
		_core: {
			element: { classList: { add: ReturnType<typeof vi.fn>; remove: ReturnType<typeof vi.fn> } };
			_selectionService: {
				enable: ReturnType<typeof vi.fn>;
				shouldForceSelection: (event: MouseEvent) => boolean;
			};
		};
	},
}));

vi.mock("@xterm/xterm", () => ({
	Terminal: class FakeTerminal {
		options: Record<string, unknown>;
		cols = 80;
		rows = 24;
		selection = "";
		keyHandler?: (event: KeyboardEvent) => boolean;
		wheelHandler?: (event: WheelEvent) => boolean;
		focus = vi.fn();
		modes = { bracketedPasteMode: false, mouseTrackingMode: "vt200" };
		lines: string[] = [];
		buffer = {
			active: {
				type: "normal",
				getLine: (i: number) => {
					const text = this.lines[i];
					return text === undefined ? undefined : { translateToString: () => text };
				},
			},
		};
		linkProvider?: FakeLinkProvider;
		linkProviders: FakeLinkProvider[] = [];
		scrollLines = vi.fn();
		dataListeners = new Set<(data: string) => void>();
		binaryListeners = new Set<(data: string) => void>();
		keyListeners = new Set<(event: { key: string }) => void>();
		selectionListeners = new Set<() => void>();
		_core = {
			element: { classList: { add: vi.fn(), remove: vi.fn() } },
			_selectionService: {
				enable: vi.fn(),
				shouldForceSelection: () => false,
			},
		};

		constructor(options: Record<string, unknown>) {
			this.options = options;
			state.lastTerminal = this;
		}

		loadAddon() {}
		open(host: HTMLElement) {
			host.appendChild(document.createElement("textarea"));
		}
		write() {}
		writeln() {}
		dispose() {}
		onData(listener: (data: string) => void) {
			this.dataListeners.add(listener);
			return { dispose: () => this.dataListeners.delete(listener) };
		}
		onBinary(listener: (data: string) => void) {
			this.binaryListeners.add(listener);
			return { dispose: () => this.binaryListeners.delete(listener) };
		}
		onResize() {
			return { dispose: () => undefined };
		}
		onRender() {
			return { dispose: () => undefined };
		}
		onKey(listener: (event: { key: string }) => void) {
			this.keyListeners.add(listener);
			return { dispose: () => this.keyListeners.delete(listener) };
		}
		registerLinkProvider(provider: FakeLinkProvider) {
			// The component registers two providers: the session-ref provider first,
			// then the SCM `#`/`!` provider. Keep `linkProvider` pointing at the first
			// (session) for the existing tests, and expose every provider via
			// `linkProviders` so the SCM tests can reach the second.
			this.linkProviders.push(provider);
			if (!this.linkProvider) this.linkProvider = provider;
			return {
				dispose: () => {
					this.linkProviders = this.linkProviders.filter((p) => p !== provider);
					this.linkProvider = this.linkProviders[0];
				},
			};
		}
		onSelectionChange(listener: () => void) {
			this.selectionListeners.add(listener);
			return { dispose: () => this.selectionListeners.delete(listener) };
		}
		hasSelection() {
			return this.selection.length > 0;
		}
		getSelection() {
			return this.selection;
		}
		attachCustomKeyEventHandler(listener: (event: KeyboardEvent) => boolean) {
			this.keyHandler = listener;
		}
		attachCustomWheelEventHandler(listener: (event: WheelEvent) => boolean) {
			this.wheelHandler = listener;
		}
		unicode = { activeVersion: "" };
	},
}));

vi.mock("@xterm/addon-fit", () => ({
	FitAddon: class FakeFitAddon {
		fit() {}
	},
}));

vi.mock("@xterm/addon-search", () => ({
	SearchAddon: class FakeSearchAddon {},
}));

vi.mock("@xterm/addon-unicode11", () => ({
	Unicode11Addon: class FakeUnicode11Addon {},
}));

vi.mock("@xterm/addon-web-links", () => ({
	WebLinksAddon: class FakeWebLinksAddon {
		constructor(handler?: (event: MouseEvent, uri: string) => void) {
			state.linkHandler = handler ?? null;
		}
	},
}));

vi.mock("@xterm/addon-canvas", () => ({
	CanvasAddon: class FakeCanvasAddon {},
}));

vi.mock("@xterm/addon-webgl", () => ({
	WebglAddon: class FakeWebglAddon {
		onContextLoss() {}
		dispose() {}
	},
}));

function setNavigatorPlatform(platform: string) {
	Object.defineProperty(window.navigator, "platform", {
		configurable: true,
		value: platform,
	});
	Object.defineProperty(window.navigator, "userAgentData", {
		configurable: true,
		value: { platform },
	});
}

describe("XtermTerminal", () => {
	beforeEach(() => {
		state.lastTerminal = null;
		state.linkHandler = null;
		setNavigatorPlatform("Linux x86_64");
		window.ao!.clipboard.writeText = vi.fn().mockResolvedValue(undefined);
		window.ao!.clipboard.readText = vi.fn().mockResolvedValue("");
	});

	it("copies selected terminal text on the terminal copy shortcut", () => {
		render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "copied selection";

		const event = {
			key: "c",
			metaKey: true,
			ctrlKey: false,
			shiftKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(false);
		expect(event.preventDefault).toHaveBeenCalled();
		expect(window.ao!.clipboard.writeText).toHaveBeenCalledWith("copied selection");
	});

	it("handles native copy events from inside the terminal", () => {
		const { container } = render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "native copied selection";
		const setData = vi.fn();
		const event = new Event("copy", { bubbles: true, cancelable: true }) as ClipboardEvent;
		Object.defineProperty(event, "clipboardData", {
			value: { setData },
		});

		container.firstElementChild!.dispatchEvent(event);

		expect(event.defaultPrevented).toBe(true);
		expect(setData).toHaveBeenCalledWith("text/plain", "native copied selection");
		expect(window.ao!.clipboard.writeText).toHaveBeenCalledWith("native copied selection");
	});

	it("copies from the focused xterm textarea when the window receives the copy shortcut", () => {
		const { container } = render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "focused copied selection";
		container.querySelector("textarea")!.focus();

		const event = new KeyboardEvent("keydown", {
			bubbles: true,
			cancelable: true,
			key: "c",
			metaKey: true,
		});
		window.dispatchEvent(event);

		expect(event.defaultPrevented).toBe(true);
		expect(window.ao!.clipboard.writeText).toHaveBeenCalledWith("focused copied selection");
	});

	it("auto-copies new selections and retries explicit copy if the auto-copy failed", async () => {
		render(<XtermTerminal theme="dark" />);
		const writeText = vi.fn().mockRejectedValueOnce(new Error("clipboard failed")).mockResolvedValueOnce(undefined);
		window.ao!.clipboard.writeText = writeText;

		state.lastTerminal!.selection = "retry me";
		state.lastTerminal!.selectionListeners.forEach((listener) => listener());
		await new Promise((resolve) => window.setTimeout(resolve, 0));

		const event = {
			key: "c",
			metaKey: true,
			ctrlKey: false,
			shiftKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(false);
		expect(writeText).toHaveBeenCalledTimes(2);
		expect(writeText).toHaveBeenLastCalledWith("retry me");
	});

	it("leaves plain Ctrl+C as terminal input on non-Windows even when text is selected", () => {
		render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "selected text";

		const event = {
			key: "c",
			metaKey: false,
			ctrlKey: true,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(true);
		expect(event.preventDefault).not.toHaveBeenCalled();
		expect(event.stopPropagation).not.toHaveBeenCalled();
		expect(window.ao!.clipboard.writeText).not.toHaveBeenCalled();
	});

	it("copies selected text with plain Ctrl+C on Windows", () => {
		setNavigatorPlatform("Win32");
		render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "windows copy";

		const event = {
			key: "c",
			metaKey: false,
			ctrlKey: true,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(false);
		expect(event.preventDefault).toHaveBeenCalled();
		expect(event.stopPropagation).toHaveBeenCalled();
		expect(window.ao!.clipboard.writeText).toHaveBeenCalledWith("windows copy");
	});

	it("leaves plain Ctrl+C as terminal input on Windows when nothing is selected", () => {
		setNavigatorPlatform("Win32");
		render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "";

		const event = {
			key: "c",
			metaKey: false,
			ctrlKey: true,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(true);
		expect(event.preventDefault).not.toHaveBeenCalled();
		expect(event.stopPropagation).not.toHaveBeenCalled();
		expect(window.ao!.clipboard.writeText).not.toHaveBeenCalled();
	});

	it.each(["Linux x86_64", "Win32"])(
		"pastes once from the Electron clipboard on Ctrl+Shift+V for %s",
		async (platform) => {
			setNavigatorPlatform(platform);
			const onInput = vi.fn();
			window.ao!.clipboard.readText = vi.fn().mockResolvedValue("hello\nworld");
			const { container } = render(
				<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />,
			);

			const event = {
				key: "v",
				metaKey: false,
				ctrlKey: true,
				shiftKey: true,
				altKey: false,
				preventDefault: vi.fn(),
				stopPropagation: vi.fn(),
			} as unknown as KeyboardEvent;
			const allowed = state.lastTerminal!.keyHandler!(event);
			const pasteEvent = new Event("paste", { bubbles: true, cancelable: true }) as ClipboardEvent;
			Object.defineProperty(pasteEvent, "clipboardData", {
				value: { getData: vi.fn().mockReturnValue("native paste") },
			});
			container.firstElementChild!.dispatchEvent(pasteEvent);
			await Promise.resolve();

			expect(allowed).toBe(false);
			expect(event.preventDefault).toHaveBeenCalled();
			expect(event.stopPropagation).toHaveBeenCalled();
			expect(window.ao!.clipboard.readText).toHaveBeenCalledTimes(1);
			expect(pasteEvent.defaultPrevented).toBe(true);
			expect(onInput).toHaveBeenCalledTimes(1);
			expect(onInput).toHaveBeenCalledWith("hello\rworld", "paste");
		},
	);

	it("supports plain Ctrl+V paste on Windows", async () => {
		setNavigatorPlatform("Win32");
		const onInput = vi.fn();
		window.ao!.clipboard.readText = vi.fn().mockResolvedValue("windows paste");
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const event = {
			key: "v",
			metaKey: false,
			ctrlKey: true,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);
		await Promise.resolve();

		expect(allowed).toBe(false);
		expect(event.preventDefault).toHaveBeenCalled();
		expect(event.stopPropagation).toHaveBeenCalled();
		expect(window.ao!.clipboard.readText).toHaveBeenCalled();
		expect(onInput).toHaveBeenCalledWith("windows paste", "paste");
	});

	it("suppresses a queued native paste event after a handled paste shortcut", async () => {
		const onInput = vi.fn();
		window.ao!.clipboard.readText = vi.fn().mockResolvedValue("shortcut paste");
		const { container } = render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const event = {
			key: "v",
			metaKey: false,
			ctrlKey: true,
			shiftKey: true,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(event)).toBe(false);
		await new Promise((resolve) => window.setTimeout(resolve, 0));

		const pasteEvent = new Event("paste", { bubbles: true, cancelable: true }) as ClipboardEvent;
		Object.defineProperty(pasteEvent, "clipboardData", {
			value: { getData: vi.fn().mockReturnValue("native paste") },
		});
		container.firstElementChild!.dispatchEvent(pasteEvent);
		await Promise.resolve();

		expect(pasteEvent.defaultPrevented).toBe(true);
		expect(onInput).toHaveBeenCalledTimes(1);
		expect(onInput).toHaveBeenCalledWith("shortcut paste", "paste");
	});

	it("supports classic Windows terminal copy and paste shortcuts", async () => {
		const onInput = vi.fn();
		window.ao!.clipboard.readText = vi.fn().mockResolvedValue("insert paste");
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		state.lastTerminal!.selection = "insert copy";

		const copyEvent = {
			key: "Insert",
			metaKey: false,
			ctrlKey: true,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(copyEvent)).toBe(false);
		expect(window.ao!.clipboard.writeText).toHaveBeenCalledWith("insert copy");

		const pasteEvent = {
			key: "Insert",
			metaKey: false,
			ctrlKey: false,
			shiftKey: true,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		expect(state.lastTerminal!.keyHandler!(pasteEvent)).toBe(false);
		await Promise.resolve();

		expect(window.ao!.clipboard.readText).toHaveBeenCalled();
		expect(onInput).toHaveBeenCalledWith("insert paste", "paste");
	});

	it.each([
		["Option/Alt+Left", { key: "ArrowLeft", altKey: true }, "\x1bb"],
		["Option/Alt+Right", { key: "ArrowRight", altKey: true }, "\x1bf"],
		["Option/Alt+Backspace", { key: "Backspace", altKey: true }, "\x1b\x7f"],
		["Option/Alt+Delete", { key: "Delete", altKey: true }, "\x1bd"],
		["Ctrl+Left", { key: "ArrowLeft", ctrlKey: true }, "\x1b[1;5D"],
		["Ctrl+Right", { key: "ArrowRight", ctrlKey: true }, "\x1b[1;5C"],
		["Ctrl+Backspace", { key: "Backspace", ctrlKey: true }, "\x1b\x7f"],
		["Ctrl+Delete", { key: "Delete", ctrlKey: true }, "\x1bd"],
	])("normalizes %s into terminal input", (_name, init, expected) => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const event = {
			metaKey: false,
			ctrlKey: false,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
			...init,
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(false);
		expect(event.preventDefault).toHaveBeenCalled();
		expect(event.stopPropagation).toHaveBeenCalled();
		expect(onInput).toHaveBeenCalledWith(expected, "shortcut");
	});

	it("sends a line feed on Shift+Enter so TUIs insert a newline instead of submitting", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const event = {
			type: "keydown",
			key: "Enter",
			metaKey: false,
			ctrlKey: false,
			shiftKey: true,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(false);
		expect(event.preventDefault).toHaveBeenCalled();
		expect(event.stopPropagation).toHaveBeenCalled();
		expect(onInput).toHaveBeenCalledWith("\n", "shortcut");
	});

	it("leaves plain Enter alone so it still submits via xterm's carriage return", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		const event = {
			type: "keydown",
			key: "Enter",
			metaKey: false,
			ctrlKey: false,
			shiftKey: false,
			altKey: false,
			preventDefault: vi.fn(),
			stopPropagation: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(true);
		expect(event.preventDefault).not.toHaveBeenCalled();
		expect(onInput).not.toHaveBeenCalled();
	});

	it("forwards keyboard input from explicit key events", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		state.lastTerminal!.keyListeners.forEach((listener) => listener({ key: "a" }));

		expect(onInput).toHaveBeenCalledWith("a", "keyboard");
	});

	it("forwards SGR mouse reports so Claude Code TUI clickables (Ran shell command, file links) register", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		// Press (M) and release (m) at a cell — xterm emits these through onData when
		// the agent has mouse tracking on; they must reach the pane.
		state.lastTerminal!.dataListeners.forEach((listener) => listener("\x1b[<0;12;7M"));
		state.lastTerminal!.dataListeners.forEach((listener) => listener("\x1b[<0;12;7m"));

		expect(onInput).toHaveBeenNthCalledWith(1, "\x1b[<0;12;7M", "mouse");
		expect(onInput).toHaveBeenNthCalledWith(2, "\x1b[<0;12;7m", "mouse");
	});

	it("forwards binary DEFAULT-encoding mouse reports (onBinary is mouse-only)", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		// DEFAULT encoding: \x1b[M + 3 bytes (button+32, col+32, row+32).
		state.lastTerminal!.binaryListeners.forEach((listener) => listener("\x1b[M !!"));

		expect(onInput).toHaveBeenCalledWith("\x1b[M !!", "mouse");
	});

	it("does not forward xterm control responses (cursor keys, DA/DSR/focus/OSC) as user input", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		// An onData listener now exists (for mouse reports), but terminal-generated
		// control responses must never be written back to the PTY — that corrupts the TUI.
		expect(state.lastTerminal!.dataListeners.size).toBeGreaterThan(0);
		for (const seq of [
			"\x1b[A", // cursor-up (arrow key echoes / control)
			"\x1b[?1;2c", // primary device attributes (DA1)
			"\x1b[>0;276;0c", // secondary device attributes (DA2)
			"\x1b[10;5R", // cursor position report (DSR)
			"\x1b[I", // focus in
			"\x1b]11;rgb:0000/0000/0000\x1b\\", // OSC color report
		]) {
			state.lastTerminal!.dataListeners.forEach((listener) => listener(seq));
		}
		expect(onInput).not.toHaveBeenCalled();
	});

	it("translates wheel motion into SGR wheel reports for zellij scrollback", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		// rowHeight = fontSize(12) * lineHeight(1.35) = 16.2px; -50px => 3 lines up.
		const suppressed = state.lastTerminal!.wheelHandler!({ deltaY: -50 } as WheelEvent);

		expect(suppressed).toBe(false);
		expect(onInput).toHaveBeenCalledWith("\x1b[<64;1;1M\x1b[<64;1;1M\x1b[<64;1;1M", "wheel");
	});

	it("handles line- and page-mode wheels (Linux/Windows mice), not just pixel deltas", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		// DOM_DELTA_LINE: deltaY is already in lines, so one notch up => one report.
		expect(state.lastTerminal!.wheelHandler!({ deltaY: -1, deltaMode: 1 } as WheelEvent)).toBe(false);
		expect(onInput).toHaveBeenLastCalledWith("\x1b[<64;1;1M", "wheel");

		// DOM_DELTA_PAGE: one page down => rows (24) line reports down.
		onInput.mockClear();
		expect(state.lastTerminal!.wheelHandler!({ deltaY: 1, deltaMode: 2 } as WheelEvent)).toBe(false);
		expect(onInput).toHaveBeenLastCalledWith("\x1b[<65;1;1M".repeat(24), "wheel");
	});

	it("scrolls down on positive wheel delta and leaves zoom (ctrl/meta) wheel alone", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		expect(state.lastTerminal!.wheelHandler!({ deltaY: 20 } as WheelEvent)).toBe(false);
		expect(onInput).toHaveBeenCalledWith("\x1b[<65;1;1M", "wheel");

		onInput.mockClear();
		expect(state.lastTerminal!.wheelHandler!({ deltaY: -50, ctrlKey: true } as WheelEvent)).toBe(false);
		expect(onInput).not.toHaveBeenCalled();
	});

	it("scrolls xterm's own viewport for normal-buffer panes with mouse tracking off (codex, plain shell)", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		state.lastTerminal!.modes.mouseTrackingMode = "none";
		state.lastTerminal!.buffer.active.type = "normal";

		// rowHeight = 16.2px; -50px => 3 lines up. The pane never sees these bytes;
		// we scroll the terminal's retained scrollback locally instead.
		expect(state.lastTerminal!.wheelHandler!({ deltaY: -50 } as WheelEvent)).toBe(false);
		expect(state.lastTerminal!.scrollLines).toHaveBeenLastCalledWith(-3);
		expect(onInput).not.toHaveBeenCalled();

		expect(state.lastTerminal!.wheelHandler!({ deltaY: 20 } as WheelEvent)).toBe(false);
		expect(state.lastTerminal!.scrollLines).toHaveBeenLastCalledWith(1);
		expect(onInput).not.toHaveBeenCalled();
	});

	it("falls back to PageUp/PageDown for alt-buffer panes with mouse tracking off", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		state.lastTerminal!.modes.mouseTrackingMode = "none";
		// Alt buffer: no local scrollback to move, and no keyboard-scroll hint, so a
		// page key per notch is the best fallback.
		state.lastTerminal!.buffer.active.type = "alternate";

		expect(state.lastTerminal!.wheelHandler!({ deltaY: -50 } as WheelEvent)).toBe(false);
		expect(onInput).toHaveBeenLastCalledWith("\x1b[5~", "wheel");
		expect(state.lastTerminal!.scrollLines).not.toHaveBeenCalled();

		expect(state.lastTerminal!.wheelHandler!({ deltaY: 20 } as WheelEvent)).toBe(false);
		expect(onInput).toHaveBeenLastCalledWith("\x1b[6~", "wheel");
	});

	it("sends SGR reports on Windows when the pane tracks the mouse (conpty delivers them to the app)", () => {
		setNavigatorPlatform("Win32");
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		// A mouse-tracking pane gets SGR reports on every platform; on Windows conpty
		// forwards them straight to the app. Keyboard-scroll panes (opencode) opt out
		// via the paneScrollsByKeyboard hint, tested separately.
		state.lastTerminal!.modes.mouseTrackingMode = "any";

		expect(state.lastTerminal!.wheelHandler!({ deltaY: -50 } as WheelEvent)).toBe(false);
		expect(onInput).toHaveBeenLastCalledWith("\x1b[<64;1;1M".repeat(3), "wheel");
	});

	it("sends PageUp/PageDown for keyboard-scroll panes even under a mux (opencode on macOS/Linux)", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" paneScrollsByKeyboard onReady={(terminal) => terminal.onUserInput(onInput)} />);
		// Linux (beforeEach) + mouse tracking on: without the paneScrollsByKeyboard
		// hint this would send SGR reports; the hint forces page keys.
		state.lastTerminal!.modes.mouseTrackingMode = "any";

		expect(state.lastTerminal!.wheelHandler!({ deltaY: -50 } as WheelEvent)).toBe(false);
		expect(onInput).toHaveBeenLastCalledWith("\x1b[5~", "wheel");
	});

	it("opens auto-detected http links via window.open so Electron routes them to the OS browser", () => {
		const open = vi.spyOn(window, "open").mockReturnValue(null);
		render(<XtermTerminal theme="dark" />);

		// The default WebLinksAddon handler opens an empty window first, which the
		// Electron main process denies; ours must pass the matched URL directly.
		expect(state.linkHandler).toBeTypeOf("function");
		state.linkHandler!({} as MouseEvent, "https://example.com");

		expect(open).toHaveBeenCalledWith("https://example.com", "_blank", "noopener");
		open.mockRestore();
	});

	it("opens http OSC 8 hyperlinks via window.open and allows non-http protocols", () => {
		const open = vi.spyOn(window, "open").mockReturnValue(null);
		render(<XtermTerminal theme="dark" />);

		const linkHandler = state.lastTerminal!.options.linkHandler as {
			activate: (event: MouseEvent, uri: string) => void;
			allowNonHttpProtocols?: boolean;
		};
		// allowNonHttpProtocols is required or xterm drops file:// links before activate.
		expect(linkHandler.allowNonHttpProtocols).toBe(true);
		linkHandler.activate({} as MouseEvent, "https://example.com/x");

		expect(open).toHaveBeenCalledWith("https://example.com/x", "_blank", "noopener");
		open.mockRestore();
	});

	it("opens file:// OSC 8 hyperlinks via the shell bridge (Electron denies window.open for file://)", () => {
		const open = vi.spyOn(window, "open").mockReturnValue(null);
		const openExternal = vi.fn().mockResolvedValue(undefined);
		window.ao!.shell.openExternal = openExternal;
		render(<XtermTerminal theme="dark" />);

		const linkHandler = state.lastTerminal!.options.linkHandler as {
			activate: (event: MouseEvent, uri: string) => void;
		};
		linkHandler.activate({} as MouseEvent, "file:///Users/me/notes/plan.md");

		expect(openExternal).toHaveBeenCalledWith("file:///Users/me/notes/plan.md");
		expect(open).not.toHaveBeenCalled();
		open.mockRestore();
	});

	it("uses native modifier-based selection so clicks reach a mouse-tracking app", () => {
		render(<XtermTerminal theme="dark" />);

		// Option (mac) / Shift still force local selection for copy — kept on.
		expect(state.lastTerminal!.options.macOptionClickForcesSelection).toBe(true);
		// The old always-force-selection override is gone: a plain (no-modifier) event
		// must NOT force selection, or xterm's mousedown handler swallows the click
		// instead of sending a mouse report to the app.
		expect(state.lastTerminal!._core._selectionService.shouldForceSelection({} as MouseEvent)).toBe(false);
	});

	it("focuses the terminal on a pointer press anywhere in the host, so one click is enough to type after using another control", () => {
		const { container } = render(<XtermTerminal theme="dark" />);
		const host = container.firstElementChild as HTMLElement;

		expect(state.lastTerminal!.focus).not.toHaveBeenCalled();
		host.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));

		expect(state.lastTerminal!.focus).toHaveBeenCalled();
	});

	it("auto-focuses the terminal on mount when autoFocus is set, so switching sessions lands the caret in the terminal", () => {
		render(<XtermTerminal theme="dark" autoFocus />);
		expect(state.lastTerminal!.focus).toHaveBeenCalled();
	});

	it("does not auto-focus on mount by default (autoFocus off)", () => {
		render(<XtermTerminal theme="dark" />);
		expect(state.lastTerminal!.focus).not.toHaveBeenCalled();
	});

	it("registers itself as the active terminal so focusTerminal() returns the caret to it, and unregisters on unmount", () => {
		const { unmount } = render(<XtermTerminal theme="dark" />);

		expect(focusTerminal()).toBe(true);
		expect(state.lastTerminal!.focus).toHaveBeenCalledTimes(1);

		unmount();
		expect(focusTerminal()).toBe(false);
	});

	describe("session-id link provider", () => {
		const known = new Set(["agent-orchestrator-54"]);
		const resolver = (line: string) =>
			findSessionLinks(line, { knownIds: known, currentProjectId: "agent-orchestrator" });

		function provideLinksFor(line: string, bufferLineNumber = 1): FakeLink[] | undefined {
			const term = state.lastTerminal!;
			term.lines = [line];
			let received: FakeLink[] | undefined;
			term.linkProvider!.provideLinks(bufferLineNumber, (links) => {
				received = links;
			});
			return received;
		}

		it("linkifies a resolved session reference with the correct 1-based buffer range", () => {
			const activate = vi.fn();
			render(<XtermTerminal theme="dark" sessionLinkResolver={resolver} onSessionLinkActivate={activate} />);

			const links = provideLinksFor("see agent-orchestrator-54 ok");
			expect(links).toHaveLength(1);
			expect(links![0].text).toBe("agent-orchestrator-54");
			// "see " is 4 chars → token at 0-based index 4 → 1-based start.x 5; the
			// token is 21 chars → inclusive end.x 25. y mirrors bufferLineNumber.
			expect(links![0].range).toEqual({ start: { x: 5, y: 1 }, end: { x: 25, y: 1 } });
		});

		it("navigates internally (not the OS browser) when a session link is activated", () => {
			const activate = vi.fn();
			render(<XtermTerminal theme="dark" sessionLinkResolver={resolver} onSessionLinkActivate={activate} />);

			const links = provideLinksFor("[from @agent-orchestrator-54] done");
			expect(links).toHaveLength(1);
			links![0].activate(new MouseEvent("click"), links![0].text);
			expect(activate).toHaveBeenCalledWith("agent-orchestrator-54");
		});

		it("returns undefined for a line with no known session reference", () => {
			render(<XtermTerminal theme="dark" sessionLinkResolver={resolver} onSessionLinkActivate={vi.fn()} />);
			// Unknown hyphen-number token (Jira key) must not linkify.
			expect(provideLinksFor("blocked by STAR-2272")).toBeUndefined();
		});

		it("returns undefined when no resolver is supplied", () => {
			render(<XtermTerminal theme="dark" />);
			expect(provideLinksFor("agent-orchestrator-54")).toBeUndefined();
		});

		it("returns undefined for a line index past the buffer", () => {
			render(<XtermTerminal theme="dark" sessionLinkResolver={resolver} onSessionLinkActivate={vi.fn()} />);
			const term = state.lastTerminal!;
			term.lines = ["agent-orchestrator-54"];
			let received: FakeLink[] | undefined = [];
			term.linkProvider!.provideLinks(5, (links) => {
				received = links;
			});
			expect(received).toBeUndefined();
		});
	});

	describe("SCM ref (#/!) link provider", () => {
		const GH = "https://github.com/acme-inc/ao-demo";
		const GL = "https://gitlab.example.com/team/webapp";
		const resolver = (line: string) => findExternalRefLinks(line, { githubRepoBase: GH, gitlabProjectBase: GL });

		// The SCM provider is registered second, so it is the last of linkProviders.
		function provideScmLinksFor(line: string, bufferLineNumber = 1): FakeLink[] | undefined {
			const term = state.lastTerminal!;
			term.lines = [line];
			const provider = term.linkProviders[term.linkProviders.length - 1]!;
			let received: FakeLink[] | undefined;
			provider.provideLinks(bufferLineNumber, (links) => {
				received = links;
			});
			return received;
		}

		it("linkifies a #<num> GitHub ref with the correct 1-based buffer range", () => {
			render(<XtermTerminal theme="dark" externalRefResolver={resolver} />);
			const links = provideScmLinksFor("opened #63 ok");
			expect(links).toHaveLength(1);
			expect(links![0].text).toBe("#63");
			// "opened " is 7 chars → token at 0-based index 7 → 1-based start.x 8; the
			// token "#63" is 3 chars → inclusive end.x 10.
			expect(links![0].range).toEqual({ start: { x: 8, y: 1 }, end: { x: 10, y: 1 } });
		});

		it("opens the GitHub PR URL in the OS browser when a #<num> ref is activated", () => {
			const open = vi.spyOn(window, "open").mockReturnValue(null);
			render(<XtermTerminal theme="dark" externalRefResolver={resolver} />);
			const links = provideScmLinksFor("see #63 now");
			links![0].activate(new MouseEvent("click"), links![0].text);
			expect(open).toHaveBeenCalledWith(`${GH}/pull/63`, "_blank", "noopener");
			open.mockRestore();
		});

		it("opens the GitLab MR URL in the OS browser when a !<num> ref is activated", () => {
			const open = vi.spyOn(window, "open").mockReturnValue(null);
			render(<XtermTerminal theme="dark" externalRefResolver={resolver} />);
			const links = provideScmLinksFor("landed !2961");
			expect(links![0].text).toBe("!2961");
			links![0].activate(new MouseEvent("click"), links![0].text);
			expect(open).toHaveBeenCalledWith(`${GL}/-/merge_requests/2961`, "_blank", "noopener");
			open.mockRestore();
		});

		it("does not linkify a hex color or #! (no false matches)", () => {
			render(<XtermTerminal theme="dark" externalRefResolver={resolver} />);
			expect(provideScmLinksFor("color: #3b82f6; #!/bin/sh")).toBeUndefined();
		});

		it("returns undefined when no external resolver is supplied", () => {
			render(<XtermTerminal theme="dark" />);
			expect(provideScmLinksFor("opened #63 ok")).toBeUndefined();
		});
	});
});
