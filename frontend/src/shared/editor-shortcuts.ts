/**
 * The editor's re-bindable shortcuts, as one canonical string each:
 * modifiers in a fixed order and then the PHYSICAL key, e.g. `Ctrl+Shift+KeyI`.
 * An empty string means unbound.
 *
 * 🗝 `Ctrl` is the Control key on every platform - it is never "Cmd on a
 * Mac". The defaults are Xcode's Re-Indent (⌃I), and Xcode's ⌃ is the
 * physical Control key; Monaco's own `CtrlCmd` would have made ⌃I mean ⌘I on
 * a Mac, which is Monaco's trigger-suggest.
 *
 * The key is `KeyboardEvent.code`, not `.key`: Shift+I reports `key` "I" and
 * an Option chord reports a symbol, while `code` is the key that was pressed
 * whatever the layout or modifiers.
 *
 * Shared by the main process (which validates what it stores) and the
 * renderer (which matches and draws them), so neither can hold a spelling the
 * other cannot read.
 */

export const MODIFIERS = ["Ctrl", "Alt", "Shift", "Meta"] as const;
export type Modifier = (typeof MODIFIERS)[number];

export const DEFAULT_REINDENT_SHORTCUT = "Ctrl+KeyI";
export const DEFAULT_FORMAT_SHORTCUT = "Ctrl+Shift+KeyI";

export type ShortcutEvent = {
	ctrlKey: boolean;
	altKey: boolean;
	shiftKey: boolean;
	metaKey: boolean;
	code: string;
};

/** A physical key a shortcut may end in. Modifier keys themselves never can. */
const KEY_CODE =
	/^(Key[A-Z]|Digit[0-9]|F([1-9]|1[0-9])|Bracket(Left|Right)|Semicolon|Quote|Comma|Period|Slash|Backslash|Backquote|Minus|Equal|Space)$/;

/**
 * The canonical spelling of a shortcut, or null when it is not one. Accepts the
 * empty string (unbound) as itself.
 */
export function normalizeShortcut(raw: unknown): string | null {
	if (raw === "") return "";
	if (typeof raw !== "string") return null;
	const parts = raw.split("+");
	const code = parts.pop() ?? "";
	if (!KEY_CODE.test(code)) return null;
	const held = new Set<string>();
	for (const part of parts) {
		if (!(MODIFIERS as readonly string[]).includes(part) || held.has(part)) return null;
		held.add(part);
	}
	return [...MODIFIERS.filter((m) => held.has(m)), code].join("+");
}

/** The shortcut a key press spells, or null while only modifiers are down. */
export function shortcutFromEvent(event: ShortcutEvent): string | null {
	if (!KEY_CODE.test(event.code)) return null;
	const held: Modifier[] = [];
	if (event.ctrlKey) held.push("Ctrl");
	if (event.altKey) held.push("Alt");
	if (event.shiftKey) held.push("Shift");
	if (event.metaKey) held.push("Meta");
	return [...held, event.code].join("+");
}

/** Whether a key press is exactly this shortcut - extra modifiers do not count as a match. */
export function matchesShortcut(event: ShortcutEvent, shortcut: string): boolean {
	return shortcut !== "" && shortcutFromEvent(event) === shortcut;
}

/**
 * Why a shortcut cannot be bound, or null when it can. A bare letter would
 * fire while typing, so anything that is not a function key needs Control,
 * Option or Command; Shift alone is still typing.
 */
export function shortcutProblem(shortcut: string): string | null {
	if (shortcut === "") return null;
	const parts = shortcut.split("+");
	const code = parts[parts.length - 1];
	if (/^F\d+$/.test(code)) return null;
	if (!parts.some((p) => p === "Ctrl" || p === "Alt" || p === "Meta")) {
		return "Add Control, Option or Command - a plain key would fire while you type.";
	}
	return null;
}

const KEY_LABELS: Record<string, string> = {
	BracketLeft: "[",
	BracketRight: "]",
	Semicolon: ";",
	Quote: "'",
	Comma: ",",
	Period: ".",
	Slash: "/",
	Backslash: "\\",
	Backquote: "`",
	Minus: "-",
	Equal: "=",
};

function keyLabel(code: string): string {
	if (code.startsWith("Key")) return code.slice(3);
	if (code.startsWith("Digit")) return code.slice(5);
	return KEY_LABELS[code] ?? code;
}

const MAC_GLYPHS: Record<Modifier, string> = { Ctrl: "⌃", Alt: "⌥", Shift: "⇧", Meta: "⌘" };
const PC_NAMES: Record<Modifier, string> = { Ctrl: "Ctrl", Alt: "Alt", Shift: "Shift", Meta: "Meta" };

/** The keycaps a shortcut is drawn as, in the platform's own order and names. */
export function shortcutKeys(shortcut: string, mac: boolean): string[] {
	if (shortcut === "") return [];
	const parts = shortcut.split("+");
	const code = parts.pop() ?? "";
	// macOS writes modifiers ⌃⌥⇧⌘ - which is MODIFIERS' own order.
	const mods = MODIFIERS.filter((m) => parts.includes(m)).map((m) => (mac ? MAC_GLYPHS[m] : PC_NAMES[m]));
	return [...mods, keyLabel(code)];
}

/** One string for a tooltip or a message: `⌃⇧I` on a Mac, `Ctrl+Shift+I` elsewhere. */
export function shortcutLabel(shortcut: string, mac: boolean): string {
	const keys = shortcutKeys(shortcut, mac);
	return mac ? keys.join("") : keys.join("+");
}

/**
 * Shortcuts something else in the app (or Monaco, or Electron's own menu)
 * already answers to, so Settings can say so before a binding silently loses
 * to it - or silently steals it.
 */
export function shortcutsTakenElsewhere(mac: boolean): { shortcut: string; what: string }[] {
	const cmd = mac ? "Meta" : "Ctrl";
	const taken = [
		{ shortcut: `${cmd}+KeyS`, what: "Save" },
		{ shortcut: `${cmd}+KeyZ`, what: "Undo" },
		{ shortcut: `${cmd}+Shift+KeyZ`, what: "Redo" },
		{ shortcut: `${cmd}+KeyF`, what: "Find" },
		{ shortcut: `${cmd}+Shift+KeyF`, what: "Search the project" },
		{ shortcut: `${cmd}+Shift+KeyO`, what: "Open Quickly" },
		{ shortcut: `${cmd}+KeyB`, what: "Toggle the sidebar" },
		{ shortcut: `${cmd}+Shift+KeyB`, what: "Toggle the inspector" },
		{ shortcut: `${cmd}+KeyA`, what: "Select All" },
		{ shortcut: `${cmd}+KeyC`, what: "Copy" },
		{ shortcut: `${cmd}+KeyV`, what: "Paste" },
		{ shortcut: `${cmd}+KeyX`, what: "Cut" },
		{ shortcut: `${cmd}+Slash`, what: "Toggle Line Comment" },
		{ shortcut: `${cmd}+BracketLeft`, what: "Outdent" },
		{ shortcut: `${cmd}+BracketRight`, what: "Indent" },
		{ shortcut: "F12", what: "Go to Definition" },
		{ shortcut: "Shift+F12", what: "Go to References" },
		{ shortcut: "Alt+F12", what: "Peek Definition" },
		{ shortcut: "Alt+Shift+KeyI", what: "Add Cursors to Line Ends" },
	];
	if (mac) {
		taken.push(
			{ shortcut: "Meta+KeyI", what: "Trigger Suggest" },
			{ shortcut: "Alt+Meta+KeyI", what: "Developer Tools" },
			{ shortcut: "Ctrl+Space", what: "Trigger Suggest" },
		);
	} else {
		// Electron's default View menu, which wins over anything the page binds.
		taken.push(
			{ shortcut: "Ctrl+Shift+KeyI", what: "Developer Tools" },
			{ shortcut: "Ctrl+KeyI", what: "Trigger Suggest" },
		);
	}
	return taken.filter((entry) => normalizeShortcut(entry.shortcut) === entry.shortcut);
}
