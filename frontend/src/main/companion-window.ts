// The desktop-companion overlay: a SECOND BrowserWindow in this same app, not a
// second app instance — so it shares the process, the `~/.ao/electron` userData
// pin and the daemon, and cannot collide with the main window's state.
//
// The Electron surface it drives is adapted to the small `OverlayWindow` interface
// below (the pattern native-notifications.ts uses), so every rule here — off by
// default, click-through, the band above the Dock, one window not two, letting go
// of a window that closed itself — is unit-testable without launching Electron.
// What CANNOT be proven from here is that macOS honours the flags; that is what
// the human smoke checklist is for.

export type OverlayBounds = { x: number; y: number; width: number; height: number };

/** The BrowserWindow constructor options the overlay needs. Asserted in the tests. */
export type OverlayWindowOptions = {
	transparent: boolean;
	frame: boolean;
	hasShadow: boolean;
	alwaysOnTop: boolean;
	resizable: boolean;
	movable: boolean;
	minimizable: boolean;
	maximizable: boolean;
	fullscreenable: boolean;
	focusable: boolean;
	skipTaskbar: boolean;
	backgroundColor: string;
	title: string;
	webPreferences: {
		preload: string;
		contextIsolation: boolean;
		nodeIntegration: boolean;
		sandbox: boolean;
	};
};

/** The subset of a BrowserWindow the overlay drives. */
export interface OverlayWindow {
	setBounds(bounds: OverlayBounds): void;
	setIgnoreMouseEvents(ignore: boolean, options?: { forward?: boolean }): void;
	setVisibleOnAllWorkspaces(visible: boolean, options?: { visibleOnFullScreen?: boolean }): void;
	setAlwaysOnTop(flag: boolean, level?: string): void;
	loadURL(url: string): Promise<void>;
	onClosed(listener: () => void): void;
	destroy(): void;
	isDestroyed(): boolean;
}

export type CompanionOverlayDeps = {
	createWindow(options: OverlayWindowOptions): OverlayWindow;
	/** The work area of the display the band lives on. Excludes the Dock and menu bar. */
	workArea(): OverlayBounds;
	overlayUrl(): string;
	preloadPath?: () => string;
	logError(message: string, error: unknown): void;
};

export type CompanionOverlay = {
	setEnabled(enabled: boolean): void;
	/** Called from the overlay renderer: true while the pointer is over a Proc. */
	setInteractive(interactive: boolean): void;
	/** Re-band after a display change. */
	relayout(): void;
	isOpen(): boolean;
	dispose(): void;
};

/**
 * Height of the floor band. Tall enough for a `full`-tier Proc (≥120px) with
 * headroom for the bubble the later PR adds, and no taller: every pixel of the
 * band is a pixel of the desktop the overlay has to forward clicks through.
 */
export const OVERLAY_BAND_HEIGHT = 190;

/**
 * The band sits flush with the bottom of the WORK AREA rather than the display.
 * On macOS the work area already excludes the Dock and the menu bar, so "above
 * the Dock" comes out of the geometry instead of a guessed inset that would be
 * wrong for every Dock size, position and auto-hide setting.
 */
export function overlayBandBounds(workArea: OverlayBounds): OverlayBounds {
	const height = Math.min(OVERLAY_BAND_HEIGHT, workArea.height);
	return {
		x: workArea.x,
		y: workArea.y + workArea.height - height,
		width: workArea.width,
		height,
	};
}

export function createCompanionOverlay(deps: CompanionOverlayDeps): CompanionOverlay {
	let win: OverlayWindow | null = null;

	const live = (): OverlayWindow | null => (win && !win.isDestroyed() ? win : null);

	const open = (): void => {
		if (live()) return;
		try {
			const created = deps.createWindow({
				transparent: true,
				frame: false,
				hasShadow: false,
				alwaysOnTop: true,
				resizable: false,
				movable: false,
				minimizable: false,
				maximizable: false,
				fullscreenable: false,
				// The overlay must never steal the keyboard from the app you are
				// working in — it is scenery you can occasionally poke, not a window.
				focusable: false,
				skipTaskbar: true,
				// Fully transparent: on a transparent window an opaque backgroundColor
				// paints a black slab over the desktop.
				backgroundColor: "#00000000",
				title: "Agent Orchestrator Companion",
				webPreferences: {
					preload: deps.preloadPath?.() ?? "",
					contextIsolation: true,
					nodeIntegration: false,
					sandbox: true,
				},
			});
			win = created;
			created.onClosed(() => {
				if (win === created) win = null;
			});
			created.setBounds(overlayBandBounds(deps.workArea()));
			// Click-through from the first frame: the desktop under the band must not
			// go dead for the moment between opening and the renderer booting.
			// `forward` still delivers move events, which is how the renderer knows
			// the pointer has reached a Proc.
			created.setIgnoreMouseEvents(true, { forward: true });
			created.setVisibleOnAllWorkspaces(true, { visibleOnFullScreen: false });
			// "floating" sits above ordinary windows but below the screen saver and
			// system UI. A higher level would cover menus and alerts.
			created.setAlwaysOnTop(true, "floating");
			void created.loadURL(deps.overlayUrl());
		} catch (err) {
			win = null;
			deps.logError("AO: failed to open the companion overlay", err);
		}
	};

	const close = (): void => {
		const current = live();
		win = null;
		if (!current) return;
		try {
			current.destroy();
		} catch (err) {
			deps.logError("AO: failed to close the companion overlay", err);
		}
	};

	return {
		setEnabled(enabled) {
			if (enabled) open();
			else close();
		},
		setInteractive(interactive) {
			const current = live();
			if (!current) return;
			if (interactive) current.setIgnoreMouseEvents(false);
			else current.setIgnoreMouseEvents(true, { forward: true });
		},
		relayout() {
			live()?.setBounds(overlayBandBounds(deps.workArea()));
		},
		isOpen() {
			return live() !== null;
		},
		dispose() {
			close();
		},
	};
}
