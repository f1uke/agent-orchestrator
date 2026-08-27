/**
 * Is this a Mac?
 *
 * Asked for two different reasons that must give the same answer: which
 * launchers exist (`open`, Ghostty, Xcode are macOS-only) and which modifier a
 * shortcut wants. `navigator.platform` is deprecated and `userAgentData` is not
 * everywhere, so the user agent is what is actually reliable in Electron and in
 * `dev:web` alike.
 */
export function isMacPlatform(): boolean {
	return typeof navigator !== "undefined" && /Mac|iPod|iPhone|iPad/.test(navigator.userAgent);
}
