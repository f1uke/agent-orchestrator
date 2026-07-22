import { contextBridge, ipcRenderer } from "electron";

// The overlay window's preload. Deliberately NOT the main renderer's preload: the
// overlay is a transparent page sitting on the desktop, and the only thing it ever
// needs from the main process is "the pointer is over a Proc, take the clicks" /
// "it left, go back to click-through". Exposing the full aoBridge there would hand
// a decorative surface the whole daemon API for no reason.
contextBridge.exposeInMainWorld("aoCompanion", {
	setInteractive: (interactive: boolean) => ipcRenderer.send("companion:setInteractive", interactive === true),
});
