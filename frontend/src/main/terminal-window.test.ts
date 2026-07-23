import { describe, expect, it } from "vitest";
import {
	createTerminalWindowHost,
	terminalBoundsFor,
	TERMINAL_ANCHOR_GAP,
	TERMINAL_WINDOW_HEIGHT,
	TERMINAL_WINDOW_WIDTH,
	TERMINAL_DETACH_TIMEOUT_MS,
	TERMINAL_MIN_HEIGHT,
	TERMINAL_MIN_WIDTH,
	type TerminalSize,
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
	/** How the page answered the detach request, and in what order it happened. */
	detachAsks: number[];
	detachAnswers: "prompt" | "never";
	events: string[];
	fireClosed(): void;
	fireResized(): void;
};

function fakeWindow(options: TerminalWindowOptions): FakeWindow {
	let closed: (() => void) | null = null;
	let resized: (() => void) | null = null;
	const win: FakeWindow = {
		options,
		bounds: [{ x: 0, y: 0, width: 720, height: 420 }],
		loaded: [],
		focused: 0,
		destroyed: false,
		detachAsks: [],
		detachAnswers: "prompt",
		events: [],
		setBounds: (bounds) => {
			win.bounds.push(bounds);
		},
		getBounds: () => win.bounds[win.bounds.length - 1],
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
		onResized: (listener) => {
			resized = listener;
		},
		requestDetach: (timeoutMs) => {
			win.detachAsks.push(timeoutMs);
			win.events.push("detach asked");
			// "never" stands for a page too wedged to answer; the real adapter gives
			// up on its own timeout, which is why this still resolves.
			return win.detachAnswers === "prompt" ? Promise.resolve() : Promise.resolve();
		},
		destroy: () => {
			win.destroyed = true;
			win.events.push("destroyed");
			closed?.();
		},
		isDestroyed: () => win.destroyed,
		fireClosed: () => closed?.(),
		fireResized: () => resized?.(),
	};
	return win;
}

function harness(options: { size?: TerminalSize | null } = {}) {
	const created: FakeWindow[] = [];
	const closedNotices: number[] = [];
	const errors: string[] = [];
	const written: TerminalSize[] = [];
	const timers: Array<{ fn: () => void; ms: number; cancelled: boolean }> = [];
	let remembered = options.size ?? null;
	const host = createTerminalWindowHost({
		createWindow: (opts) => {
			const win = fakeWindow(opts);
			created.push(win);
			return win;
		},
		urlFor: (sessionId, handleId) => `app://renderer/companion.html?terminalFor=${sessionId}&handle=${handleId}`,
		workAreaNear: () => WORK_AREA,
		preloadPath: () => "/preload.js",
		readSize: () => remembered,
		writeSize: (size) => {
			remembered = size;
			written.push(size);
		},
		onClosed: () => closedNotices.push(1),
		logError: (message) => errors.push(message),
		setTimer: (fn, ms) => {
			const entry = { fn, ms, cancelled: false };
			timers.push(entry);
			return entry;
		},
		clearTimer: (handle) => {
			(handle as { cancelled: boolean }).cancelled = true;
		},
	});
	const runIdleTimer = () => {
		const live = timers.filter((t) => !t.cancelled);
		live[live.length - 1]?.fn();
	};
	return {
		host,
		created,
		closedNotices,
		errors,
		written,
		timers,
		runIdleTimer,
		last: () => created[created.length - 1],
	};
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

		expect(last().options).toMatchObject({ frame: false, transparent: true, alwaysOnTop: true, resizable: true });
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
		expect(last().bounds.at(-1)).toEqual(terminalBoundsFor(OPEN.anchor, WORK_AREA));
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

describe("handing a pane over without ever attaching it twice", () => {
	it("asks the page to let go, and only destroys the window once it has", async () => {
		const { host, last } = harness();
		host.open(OPEN);

		await host.closeForHandoff();

		// The ORDER is the safety property: ask, then destroy. A window destroyed
		// first takes its renderer with it, and the pane is never told anything.
		expect(last().events).toEqual(["detach asked", "destroyed"]);
		expect(last().detachAsks).toEqual([TERMINAL_DETACH_TIMEOUT_MS]);
		expect(host.isOpen()).toBe(false);
	});

	it("still closes when the page cannot answer", async () => {
		// A wedged renderer must not be able to hold the board window shut.
		const { host, last } = harness();
		host.open(OPEN);
		last().detachAnswers = "never";

		await host.closeForHandoff();

		expect(last().destroyed).toBe(true);
	});

	it("handing off when nothing is open is a no-op", async () => {
		const { host, created, errors } = harness();

		await host.closeForHandoff();

		expect(created).toHaveLength(0);
		expect(errors).toEqual([]);
	});
});

describe("the size the human chose", () => {
	it("opens at the remembered size", () => {
		const { host, last } = harness({ size: { width: 900, height: 560 } });
		host.open(OPEN);

		expect(last().bounds.at(-1)).toMatchObject({ width: 900, height: 560 });
	});

	it("remembers a size once the drag has settled", () => {
		const { host, last, written } = harness();
		host.open(OPEN);
		last().bounds.push({ x: 0, y: 0, width: 980, height: 610 });

		last().fireResized();

		expect(written).toEqual([{ width: 980, height: 610 }]);
	});

	it("keeps the size while following its Proc", () => {
		const { host, last } = harness({ size: { width: 900, height: 560 } });
		host.open(OPEN);

		host.moveTo({ x: 300, y: 700 });

		expect(last().bounds.at(-1)).toMatchObject({ width: 900, height: 560 });
	});

	it("never opens smaller than a terminal you can read", () => {
		const { host, last } = harness({ size: { width: 40, height: 20 } });
		host.open(OPEN);

		expect(last().bounds.at(-1)!.width).toBeGreaterThanOrEqual(TERMINAL_MIN_WIDTH);
		expect(last().bounds.at(-1)!.height).toBeGreaterThanOrEqual(TERMINAL_MIN_HEIGHT);
	});
});

describe("a terminal left open all night", () => {
	it("closes itself once nobody has touched it for a long time", () => {
		const { host, last, runIdleTimer } = harness();
		host.open(OPEN);

		runIdleTimer();

		expect(last().destroyed).toBe(true);
		expect(host.isOpen()).toBe(false);
	});

	it("starts the clock again every time the human types", () => {
		const { host, timers, runIdleTimer, last } = harness();
		host.open(OPEN);
		const first = timers[0];

		host.noteActivity();

		expect(first.cancelled).toBe(true);
		runIdleTimer();
		expect(last().destroyed).toBe(true);
	});

	it("does not start a clock for a terminal that is not open", () => {
		const { host, timers } = harness();

		host.noteActivity();

		expect(timers).toHaveLength(0);
	});
});
