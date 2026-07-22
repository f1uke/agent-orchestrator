import { describe, expect, it } from "vitest";
import { COMPANION_CONTENT_HEIGHT, petFrame } from "../companion/layout";
import {
	OVERLAY_BAND_HEIGHT,
	createCompanionOverlay,
	overlayBandBounds,
	type OverlayWindow,
	type OverlayWindowOptions,
} from "./companion-window";

const WORK_AREA = { x: 0, y: 25, width: 1440, height: 875 };

type FakeWindow = OverlayWindow & {
	options: OverlayWindowOptions;
	bounds: { x: number; y: number; width: number; height: number } | null;
	ignoreMouse: Array<{ ignore: boolean; forward?: boolean }>;
	allWorkspaces: Array<{ visible: boolean; visibleOnFullScreen?: boolean }>;
	alwaysOnTop: Array<{ flag: boolean; level?: string }>;
	loaded: string[];
	destroyed: boolean;
	fireClosed(): void;
};

function fakeWindow(options: OverlayWindowOptions): FakeWindow {
	let closed: (() => void) | null = null;
	const win: FakeWindow = {
		options,
		bounds: null,
		ignoreMouse: [],
		allWorkspaces: [],
		alwaysOnTop: [],
		loaded: [],
		destroyed: false,
		setBounds: (b) => {
			win.bounds = b;
		},
		setIgnoreMouseEvents: (ignore, opts) => win.ignoreMouse.push({ ignore, forward: opts?.forward }),
		setVisibleOnAllWorkspaces: (visible, opts) =>
			win.allWorkspaces.push({ visible, visibleOnFullScreen: opts?.visibleOnFullScreen }),
		setAlwaysOnTop: (flag, level) => win.alwaysOnTop.push({ flag, level }),
		loadURL: (url) => {
			win.loaded.push(url);
			return Promise.resolve();
		},
		onClosed: (cb) => {
			closed = cb;
		},
		destroy: () => {
			win.destroyed = true;
		},
		isDestroyed: () => win.destroyed,
		fireClosed: () => closed?.(),
	};
	return win;
}

function harness(workArea = WORK_AREA) {
	const created: FakeWindow[] = [];
	const errors: string[] = [];
	const overlay = createCompanionOverlay({
		createWindow: (options) => {
			const win = fakeWindow(options);
			created.push(win);
			return win;
		},
		workArea: () => workArea,
		overlayUrl: () => "app://renderer/companion.html",
		logError: (message) => errors.push(message),
	});
	return { overlay, created, errors, last: () => created[created.length - 1] };
}

describe("OVERLAY_BAND_HEIGHT", () => {
	it("is tall enough for everything the overlay draws", () => {
		// It was guessed at 190px once. A Proc's hover tooltip reaches 218px above the
		// floor, so it was clipped off the top of the window and hovering appeared to
		// do nothing at all. The band is derived from the art now, and this is what
		// stops it drifting back to a number somebody liked the look of.
		expect(OVERLAY_BAND_HEIGHT).toBeGreaterThanOrEqual(COMPANION_CONTENT_HEIGHT);
		expect(OVERLAY_BAND_HEIGHT).toBeGreaterThanOrEqual(petFrame().height + 60);
	});

	it("is no taller than it needs to be", () => {
		// Every pixel of the band is a pixel of desktop the overlay has to forward
		// clicks through.
		expect(OVERLAY_BAND_HEIGHT).toBeLessThanOrEqual(COMPANION_CONTENT_HEIGHT + 8);
	});
});

describe("overlayBandBounds", () => {
	it("is a band across the bottom of the work area, which already excludes the Dock", () => {
		expect(overlayBandBounds(WORK_AREA)).toEqual({
			x: 0,
			y: 25 + 875 - OVERLAY_BAND_HEIGHT,
			width: 1440,
			height: OVERLAY_BAND_HEIGHT,
		});
	});

	it("never grows taller than the display it sits on", () => {
		const tiny = { x: 0, y: 0, width: 800, height: 120 };

		expect(overlayBandBounds(tiny)).toEqual({ x: 0, y: 0, width: 800, height: 120 });
	});
});

describe("createCompanionOverlay", () => {
	it("creates nothing until it is enabled — off by default", () => {
		const { overlay, created } = harness();

		expect(created).toHaveLength(0);
		expect(overlay.isOpen()).toBe(false);
	});

	it("opens a transparent, frameless, shadowless always-on-top window", () => {
		const { overlay, last } = harness();
		overlay.setEnabled(true);
		const { options } = last();

		expect(options.transparent).toBe(true);
		expect(options.frame).toBe(false);
		expect(options.hasShadow).toBe(false);
		expect(options.alwaysOnTop).toBe(true);
		expect(options.resizable).toBe(false);
		expect(options.focusable).toBe(false);
		expect(options.skipTaskbar).toBe(true);
		expect(options.backgroundColor).toBe("#00000000");
	});

	it("keeps the renderer sandboxed and context-isolated like the main window", () => {
		const { overlay, last } = harness();
		overlay.setEnabled(true);

		expect(last().options.webPreferences).toMatchObject({
			contextIsolation: true,
			nodeIntegration: false,
			sandbox: true,
		});
	});

	it("starts click-through, so the desktop underneath keeps working", () => {
		const { overlay, last } = harness();
		overlay.setEnabled(true);

		expect(last().ignoreMouse[0]).toEqual({ ignore: true, forward: true });
	});

	it("follows the user across Spaces but yields to a fullscreen app", () => {
		const { overlay, last } = harness();
		overlay.setEnabled(true);

		expect(last().allWorkspaces[0]).toEqual({ visible: true, visibleOnFullScreen: false });
	});

	it("floats above ordinary windows without taking over the screen", () => {
		const { overlay, last } = harness();
		overlay.setEnabled(true);

		expect(last().alwaysOnTop[0]).toEqual({ flag: true, level: "floating" });
	});

	it("sits in the band above the Dock and loads the overlay page", () => {
		const { overlay, last } = harness();
		overlay.setEnabled(true);

		expect(last().bounds).toEqual(overlayBandBounds(WORK_AREA));
		expect(last().loaded).toEqual(["app://renderer/companion.html"]);
	});

	it("opens exactly one window however often it is enabled", () => {
		const { overlay, created } = harness();
		overlay.setEnabled(true);
		overlay.setEnabled(true);

		expect(created).toHaveLength(1);
	});

	it("closes the overlay when it is turned off, and reopens on a later enable", () => {
		const { overlay, created } = harness();
		overlay.setEnabled(true);
		overlay.setEnabled(false);

		expect(created[0].destroyed).toBe(true);
		expect(overlay.isOpen()).toBe(false);

		overlay.setEnabled(true);
		expect(created).toHaveLength(2);
		expect(overlay.isOpen()).toBe(true);
	});

	it("turning it off when it was never on is a no-op", () => {
		const { overlay, created, errors } = harness();
		overlay.setEnabled(false);

		expect(created).toHaveLength(0);
		expect(errors).toEqual([]);
	});

	it("lets go of a window that closed on its own", () => {
		const { overlay, created } = harness();
		overlay.setEnabled(true);
		created[0].fireClosed();

		expect(overlay.isOpen()).toBe(false);
		overlay.setEnabled(true);
		expect(created).toHaveLength(2);
	});
});

describe("click-through", () => {
	it("takes the pointer only while it is over a Proc, and gives it straight back", () => {
		const { overlay, last } = harness();
		overlay.setEnabled(true);

		overlay.setInteractive(true);
		overlay.setInteractive(false);

		expect(last().ignoreMouse).toEqual([
			{ ignore: true, forward: true },
			{ ignore: false, forward: undefined },
			{ ignore: true, forward: true },
		]);
	});

	it("ignores an interaction request when the overlay is closed", () => {
		const { overlay, errors } = harness();

		expect(() => overlay.setInteractive(true)).not.toThrow();
		expect(errors).toEqual([]);
	});
});

describe("relayout", () => {
	it("re-bands the overlay when the display geometry changes", () => {
		let area = { ...WORK_AREA };
		const created: FakeWindow[] = [];
		const overlay = createCompanionOverlay({
			createWindow: (options) => {
				const win = fakeWindow(options);
				created.push(win);
				return win;
			},
			workArea: () => area,
			overlayUrl: () => "app://renderer/companion.html",
			logError: () => {},
		});
		overlay.setEnabled(true);

		area = { x: 0, y: 25, width: 2560, height: 1415 };
		overlay.relayout();

		expect(created[0].bounds).toEqual(overlayBandBounds(area));
	});

	it("is a no-op when the overlay is closed", () => {
		const { overlay } = harness();

		expect(() => overlay.relayout()).not.toThrow();
	});
});
