import { describe, expect, it } from "vitest";
import {
	DEFAULT_FORMAT_SHORTCUT,
	DEFAULT_REINDENT_SHORTCUT,
	matchesShortcut,
	normalizeShortcut,
	shortcutFromEvent,
	shortcutKeys,
	shortcutLabel,
	shortcutProblem,
	shortcutsTakenElsewhere,
} from "./editor-shortcuts";

const press = (code: string, mods: Partial<Record<"ctrlKey" | "altKey" | "shiftKey" | "metaKey", boolean>> = {}) => ({
	ctrlKey: false,
	altKey: false,
	shiftKey: false,
	metaKey: false,
	...mods,
	code,
});

describe("editor shortcuts", () => {
	it("spells a key press canonically, by physical key", () => {
		expect(shortcutFromEvent(press("KeyI", { ctrlKey: true }))).toBe("Ctrl+KeyI");
		expect(shortcutFromEvent(press("KeyI", { shiftKey: true, ctrlKey: true }))).toBe("Ctrl+Shift+KeyI");
		expect(shortcutFromEvent(press("ControlLeft", { ctrlKey: true }))).toBeNull();
	});

	it("matches exactly: Control is never Command, and extra modifiers do not match", () => {
		expect(matchesShortcut(press("KeyI", { ctrlKey: true }), DEFAULT_REINDENT_SHORTCUT)).toBe(true);
		expect(matchesShortcut(press("KeyI", { metaKey: true }), DEFAULT_REINDENT_SHORTCUT)).toBe(false);
		expect(matchesShortcut(press("KeyI", { ctrlKey: true, shiftKey: true }), DEFAULT_REINDENT_SHORTCUT)).toBe(false);
		expect(matchesShortcut(press("KeyI", { ctrlKey: true, shiftKey: true }), DEFAULT_FORMAT_SHORTCUT)).toBe(true);
		expect(matchesShortcut(press("KeyI", { ctrlKey: true }), "")).toBe(false);
	});

	it("normalizes modifier order and rejects what it cannot read", () => {
		expect(normalizeShortcut("Shift+Ctrl+KeyI")).toBe("Ctrl+Shift+KeyI");
		expect(normalizeShortcut("")).toBe("");
		expect(normalizeShortcut("Ctrl+Ctrl+KeyI")).toBeNull();
		expect(normalizeShortcut("Hyper+KeyI")).toBeNull();
		expect(normalizeShortcut("Ctrl+ShiftLeft")).toBeNull();
		expect(normalizeShortcut(42)).toBeNull();
	});

	it("draws keycaps the way each platform writes them", () => {
		expect(shortcutKeys(DEFAULT_FORMAT_SHORTCUT, true)).toEqual(["⌃", "⇧", "I"]);
		expect(shortcutLabel(DEFAULT_FORMAT_SHORTCUT, true)).toBe("⌃⇧I");
		expect(shortcutLabel(DEFAULT_FORMAT_SHORTCUT, false)).toBe("Ctrl+Shift+I");
		expect(shortcutLabel("Alt+Meta+BracketLeft", true)).toBe("⌥⌘[");
	});

	it("refuses a binding that would fire while typing", () => {
		expect(shortcutProblem("KeyI")).not.toBeNull();
		expect(shortcutProblem("Shift+KeyI")).not.toBeNull();
		expect(shortcutProblem("F5")).toBeNull();
		expect(shortcutProblem(DEFAULT_REINDENT_SHORTCUT)).toBeNull();
	});

	it("leaves the defaults free on a Mac, and knows Electron takes Ctrl+Shift+I elsewhere", () => {
		const mac = shortcutsTakenElsewhere(true).map((t) => t.shortcut);
		expect(mac).not.toContain(DEFAULT_REINDENT_SHORTCUT);
		expect(mac).not.toContain(DEFAULT_FORMAT_SHORTCUT);
		expect(mac).toContain("Meta+KeyI");
		const pc = shortcutsTakenElsewhere(false);
		expect(pc.find((t) => t.shortcut === DEFAULT_FORMAT_SHORTCUT)?.what).toBe("Developer Tools");
	});
});
