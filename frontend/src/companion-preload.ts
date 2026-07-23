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
	// PROTOTYPE (terminal bubble). A plain LEFT click on a Proc: "let me talk to
	// this session." `invoke`, not `send`, because the answer decides what the
	// overlay draws — and the answer is the main process's to give: only it knows
	// whether the board window is up, and only it can bring that window forward.
	activateSession: (sessionId: string) =>
		ipcRenderer.invoke("companion:activateSession", sessionId) as Promise<"app" | "bubble" | "unavailable">,
	/** The bubble closed. Hand the keyboard back to whatever the user was in. */
	releaseKeyboard: () => ipcRenderer.send("companion:releaseKeyboard"),
	/** PROTOTYPE harness only (no-op unless the main process was asked for it). */
	protoOpenMainWindow: () => ipcRenderer.send("companion:protoOpenMainWindow"),
	/** The board window came up: detach the bubble's terminal before it attaches. */
	onMainWindowOpened: (listener: () => void) => {
		const wrapped = () => listener();
		ipcRenderer.on("companion:mainWindowOpened", wrapped);
		return () => {
			ipcRenderer.off("companion:mainWindowOpened", wrapped);
		};
	},
});
