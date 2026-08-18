import { describe, expect, it } from "vitest";
import { classifyKey } from "./useDeviceKeyboard";

const press = (key: string, mods: Partial<{ metaKey: boolean; ctrlKey: boolean; altKey: boolean }> = {}) => ({
	key,
	metaKey: false,
	ctrlKey: false,
	altKey: false,
	...mods,
});

describe("what a keypress means to the device", () => {
	// 🗝 The character comes from event.key, already resolved through the
	// human's input source. This is the whole reason the tab can be correct
	// where a synthesised US keycode cannot: on a Thai Mac the key labelled `f`
	// reports "ด", and "ด" is what must reach the device.
	it("takes the character the human meant, whatever the input source", () => {
		expect(classifyKey(press("a"))).toEqual({ kind: "text", text: "a" });
		expect(classifyKey(press("A"))).toEqual({ kind: "text", text: "A" });
		expect(classifyKey(press(" "))).toEqual({ kind: "text", text: " " });
		expect(classifyKey(press("ด"))).toEqual({ kind: "text", text: "ด" });
		expect(classifyKey(press("ก"))).toEqual({ kind: "text", text: "ก" });
		// A Thai tone mark is its own keystroke and its own character.
		expect(classifyKey(press("่"))).toEqual({ kind: "text", text: "่" });
		expect(classifyKey(press("é"))).toEqual({ kind: "text", text: "é" });
		// Outside the basic plane: one character, not two code units.
		expect(classifyKey(press("😀"))).toEqual({ kind: "text", text: "😀" });
	});

	it("sends the keys that produce no character as named keys", () => {
		expect(classifyKey(press("Enter"))).toEqual({ kind: "key", name: "enter" });
		expect(classifyKey(press("Backspace"))).toEqual({ kind: "key", name: "backspace" });
		expect(classifyKey(press("Tab"))).toEqual({ kind: "key", name: "tab" });
		expect(classifyKey(press("ArrowUp"))).toEqual({ kind: "key", name: "arrow-up" });
		expect(classifyKey(press("ArrowDown"))).toEqual({ kind: "key", name: "arrow-down" });
		expect(classifyKey(press("ArrowLeft"))).toEqual({ kind: "key", name: "arrow-left" });
		expect(classifyKey(press("ArrowRight"))).toEqual({ kind: "key", name: "arrow-right" });
	});

	// ⚠ The rule that keeps the rest of AO usable. A pane that swallowed ⌘W
	// would be a worse bug than the one this fixes.
	it("never takes a shortcut", () => {
		for (const mods of [{ metaKey: true }, { ctrlKey: true }, { altKey: true }]) {
			expect(classifyKey(press("w", mods))).toBeNull();
			expect(classifyKey(press("k", mods))).toBeNull();
			expect(classifyKey(press("Enter", mods))).toBeNull();
			expect(classifyKey(press("ArrowLeft", mods))).toBeNull();
		}
	});

	// Shift is not a shortcut modifier: it is how a capital is typed, and
	// event.key already carries the result.
	it("still types a capital letter", () => {
		expect(classifyKey({ ...press("A"), metaKey: false })).toEqual({ kind: "text", text: "A" });
	});

	it("leaves alone what it cannot mean", () => {
		for (const key of ["F5", "Home", "PageUp", "Shift", "Meta", "CapsLock", "Insert", "Escape"]) {
			expect(classifyKey(press(key)), key).toBeNull();
		}
	});

	// "Dead" is the first half of a composed character. Direct input sources -
	// Thai, Latin, Cyrillic - never produce it for ordinary text; an IME with a
	// candidate window does not deliver its result through keydown at all. That
	// input source is unsupported and is left alone rather than half-handled.
	it("does not mistake a composition event for a character", () => {
		expect(classifyKey(press("Dead"))).toBeNull();
		expect(classifyKey(press("Process"))).toBeNull();
		expect(classifyKey(press("Unidentified"))).toBeNull();
	});
});
