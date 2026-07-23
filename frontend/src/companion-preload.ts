import { contextBridge, ipcRenderer } from "electron";

// The overlay window's preload. Deliberately NOT the main renderer's preload: the
// overlay is a transparent page sitting on the desktop, and the only thing it ever
// needs from the main process is "the pointer is over a Proc, take the clicks" /
// "it left, go back to click-through". Exposing the full aoBridge there would hand
// a decorative surface the whole daemon API for no reason.
contextBridge.exposeInMainWorld("aoCompanion", {
	setInteractive: (interactive: boolean) => ipcRenderer.send("companion:setInteractive", interactive === true),
	// The overlay is its own page with no daemon connection of its own, and the
	// daemon's port is discovered rather than fixed — so the main process, which
	// already knows it, hands it over. Null until the daemon is up, which is why the
	// overlay retries rather than assuming a port.
	daemonUrl: () => ipcRenderer.invoke("companion:daemonUrl") as Promise<string | null>,
	// Right-click on a Proc. The overlay cannot open a window itself, and the
	// library belongs in the main one anyway - see the handler in main.ts.
	requestLook: (sessionId: string) => ipcRenderer.send("companion:requestLook", sessionId),
	// "Go and re-read the chosen looks." Carries no data: the looks live in the
	// localStorage both windows share, and this is only a second way of being told
	// to look, alongside the `storage` event.
	onLooksChanged: (listener: () => void) => {
		const wrapped = () => listener();
		ipcRenderer.on("companion:looksChanged", wrapped);
		return () => {
			ipcRenderer.off("companion:looksChanged", wrapped);
		};
	},
	// A plain LEFT click on a Proc: "let me talk to this session." `invoke`, not
	// `send`, because the answer decides what the overlay does next — and the answer
	// is the main process's to give: only it knows whether the board window is up,
	// and only it can bring that window forward or put a terminal on the desktop.
	activateSession: (input: { sessionId: string; handleId: string; anchor: { x: number; y: number } }) =>
		ipcRenderer.invoke("companion:activateSession", input) as Promise<"app" | "bubble" | "unavailable">,
	/** The Proc moved; carry its terminal window along. */
	moveTerminal: (anchor: { x: number; y: number }) => ipcRenderer.send("companion:moveTerminal", anchor),
	/** Close the terminal window — from the card itself, or from the band. */
	closeTerminal: () => ipcRenderer.send("companion:closeTerminal"),
	/** The terminal window went away, however it went. */
	onTerminalClosed: (listener: () => void) => {
		const wrapped = () => listener();
		ipcRenderer.on("companion:terminalClosed", wrapped);
		return () => {
			ipcRenderer.off("companion:terminalClosed", wrapped);
		};
	},
	/** The board window came up: the overlay stops following a terminal that is going. */
	onMainWindowOpened: (listener: () => void) => {
		const wrapped = () => listener();
		ipcRenderer.on("companion:mainWindowOpened", wrapped);
		return () => {
			ipcRenderer.off("companion:mainWindowOpened", wrapped);
		};
	},
	// ─── the terminal window's own half of the bridge ───────────────────────────
	/** Which way its Proc is, so the card's tail points at it. */
	terminalTail: () => ipcRenderer.invoke("terminal:tail") as Promise<"left" | "centre" | "right">,
	/** The human typed in it, so its idle clock starts again. */
	noteTerminalActivity: () => ipcRenderer.send("terminal:activity"),
	/**
	 * "Let the pane go, now."
	 *
	 * Asked before the window is destroyed, because destroying it kills the renderer
	 * where it stands and the pane would never be told. The answer is what makes
	 * "never attached in two places" true by order rather than by luck.
	 */
	onDetachRequest: (listener: () => void) => {
		const wrapped = () => listener();
		ipcRenderer.on("terminal:detach", wrapped);
		return () => {
			ipcRenderer.off("terminal:detach", wrapped);
		};
	},
	reportDetached: () => ipcRenderer.send("terminal:detached"),
});
