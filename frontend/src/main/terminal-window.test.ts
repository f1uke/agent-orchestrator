import { describe, expect, it } from "vitest";
import {
	createTerminalWindowHost,
	terminalBoundsFor,
	TERMINAL_ANCHOR_GAP,
	TERMINAL_WINDOW_HEIGHT,
	TERMINAL_WINDOW_WIDTH,
	type TerminalWindow,
	type TerminalWindowOptions,
} from "./terminal-window";

const WORK_AREA = { x: 0, y: 25, width: 1440, height: 875 };

type FakeWindow = TerminalWindow & {
	options: TerminalWindowOptions;
	bounds: Array<{ x: number; y: number; width: number; height: number }>;
	loaded: string[];
	focused: number;
	destroyed: boolean;
	fireClosed(): void;
};

function fakeWindow(options: TerminalWindowOptions): FakeWindow {
	let closed: (() => void) | null = null;
	const win: FakeWindow = {
		options,
		bounds: [],
		loaded: [],
		focused: 0,
		destroyed: false,
		setBounds: (bounds) => win.bounds.push(bounds),
		loadURL: (url) => {
			win.loaded.push(url);
			return Promise.resolve();
		},
		focus: () => {
			win.focused += 1;
		},
		onClosed: (listener) => {
			closed = listener;
		},
		destroy: () => {
			win.destroyed = true;
			closed?.();
		},
		isDestroyed: () => win.destroyed,
		fireClosed: () => closed?.(),
	};
	return win;
}

function harness() {
	const created: FakeWindow[] = [];
	const closedNotices: number[] = [];
	const errors: string[] = [];
	const host = createTerminalWindowHost({
		createWindow: (options) => {
			const win = fakeWindow(options);
			created.push(win);
			return win;
		},
		urlFor: (sessionId, handleId) => `app://renderer/companion.html?terminalFor=${sessionId}&handle=${handleId}`,
		workArea: () => WORK_AREA,
		preloadPath: () => "/preload.js",
		onClosed: () => closedNotices.push(1),
		logError: (message) => errors.push(message),
	});
	return { host, created, closedNotices, errors, last: () => created[created.length - 1] };
}

const OPEN = { sessionId: "s1", handleId: "pane-1", anchor: { x: 700, y: 880 } };

describe("terminalBoundsFor", () => {
	it("centres the card on its Proc and lifts it clear of the figure", () => {
		const bounds = terminalBoundsFor({ x: 700, y: 880 }, WORK_AREA);

		expect(bounds.x + bounds.width / 2).toBe(700);
		expect(bounds.y + bounds.height).toBe(880 - TERMINAL_ANCHOR_GAP);
		expect(bounds.width).toBe(TERMINAL_WINDOW_WIDTH);
		expect(bounds.height).toBe(TERMINAL_WINDOW_HEIGHT);
	});

	it("keeps the whole card on screen for a Proc at either edge", () => {
		// A terminal with lines running off the display is a terminal you cannot read.
		const left = terminalBoundsFor({ x: 10, y: 880 }, WORK_AREA);
		const right = terminalBoundsFor({ x: 1430, y: 880 }, WORK_AREA);

		expect(left.x).toBeGreaterThanOrEqual(WORK_AREA.x);
		expect(right.x + right.width).toBeLessThanOrEqual(WORK_AREA.x + WORK_AREA.width);
	});

	it("stays inside the work area for a Proc standing high up after a throw", () => {
		const bounds = terminalBoundsFor({ x: 700, y: 40 }, WORK_AREA);

		expect(bounds.y).toBeGreaterThanOrEqual(WORK_AREA.y);
		expect(bounds.y + bounds.height).toBeLessThanOrEqual(WORK_AREA.y + WORK_AREA.height);
	});

	it("shrinks rather than overflowing a display smaller than the card", () => {
		const tiny = { x: 0, y: 0, width: 400, height: 300 };
		const bounds = terminalBoundsFor({ x: 200, y: 280 }, tiny);

		expect(bounds.width).toBeLessThanOrEqual(tiny.width);
		expect(bounds.height).toBeLessThanOrEqual(tiny.height);
		expect(bounds.x).toBeGreaterThanOrEqual(tiny.x);
	});
});

describe("the terminal window", () => {
	it("is a NORMAL window: it can be focused, and it is not the overlay", () => {
		// The whole reason it exists. The overlay may never be made focusable — doing
		// that blinked every Proc off the screen and cost the band its mouse events.
		const { host, last } = harness();
		host.open(OPEN);

		expect(last().options).toMatchObject({ frame: false, transparent: true, alwaysOnTop: true, resizable: false });
		expect(last().options.webPreferences).toMatchObject({
			contextIsolation: true,
			nodeIntegration: false,
			sandbox: true,
		});
		expect("focusable" in last().options).toBe(false);
	});

	it("opens on the session and pane it was given, and takes the keyboard", () => {
		const { host, last } = harness();
		host.open(OPEN);

		expect(last().loaded).toEqual(["app://renderer/companion.html?terminalFor=s1&handle=pane-1"]);
		expect(last().bounds[0]).toEqual(terminalBoundsFor(OPEN.anchor, WORK_AREA));
		expect(last().focused).toBe(1);
		expect(host.openFor()).toBe("s1");
	});

	it("keeps exactly one open: a second session replaces the first", () => {
		// One keyboard, one caret — and one attach per pane.
		const { host, created } = harness();
		host.open(OPEN);
		host.open({ sessionId: "s2", handleId: "pane-2", anchor: { x: 300, y: 880 } });

		expect(created).toHaveLength(2);
		expect(created[0].destroyed).toBe(true);
		expect(host.openFor()).toBe("s2");
	});

	it("moves with its Proc", () => {
		const { host, last } = harness();
		host.open(OPEN);

		host.moveTo({ x: 300, y: 700 });

		expect(last().bounds.at(-1)).toEqual(terminalBoundsFor({ x: 300, y: 700 }, WORK_AREA));
	});

	it("ignores a move when nothing is open", () => {
		const { host, created, errors } = harness();

		host.moveTo({ x: 300, y: 700 });

		expect(created).toHaveLength(0);
		expect(errors).toEqual([]);
	});

	it("reports its closing however it closed, so the band lets its Proc wander again", () => {
		const { host, created, closedNotices } = harness();
		host.open(OPEN);

		created[0].fireClosed();

		expect(closedNotices).toHaveLength(1);
		expect(host.isOpen()).toBe(false);
		expect(host.openFor()).toBeNull();
	});

	it("closing twice is not an error, and closing when never opened is not either", () => {
		const { host, errors } = harness();
		host.open(OPEN);
		host.close();

		expect(() => host.close()).not.toThrow();
		expect(errors).toEqual([]);
	});
});
