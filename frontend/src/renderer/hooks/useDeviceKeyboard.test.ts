import { describe, expect, it } from "vitest";
import { classifyKey } from "./useDeviceKeyboard";

const press = (
	key: string,
	mods: Partial<{
		metaKey: boolean;
		ctrlKey: boolean;
		altKey: boolean;
		shiftKey: boolean;
		code: string;
		capsLock: boolean;
	}> = {},
) => ({
	key,
	metaKey: false,
	ctrlKey: false,
	altKey: false,
	...mods,
	getModifierState: (name: "CapsLock") => name === "CapsLock" && mods.capsLock === true,
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

/**
 * 🗝 The character says what the human meant; the KEY says what they did. Both
 * travel, because they answer different questions: the key is how the keystroke
 * reaches the device at the speed of Simulator.app, and the character is what a
 * recording keeps and what the daemon delivers if the key cannot be forwarded.
 */
describe("the key a character came from", () => {
	/** classifyKey, narrowed to the character case the tests below are about. */
	const character = (event: Parameters<typeof classifyKey>[0]) => {
		const what = classifyKey(event);
		if (what?.kind !== "text") throw new Error(`expected a character, got ${JSON.stringify(what)}`);
		return what;
	};

	it("travels with the character it produced, whatever the layout printed", () => {
		// A Thai Mac: the key a US keyboard prints `f` on produced "ด".
		expect(classifyKey(press("ด", { code: "KeyF" }))).toEqual({
			kind: "text",
			text: "ด",
			key: { code: "KeyF", shift: false },
		});
		expect(classifyKey(press("a", { code: "KeyA" }))).toEqual({
			kind: "text",
			text: "a",
			key: { code: "KeyA", shift: false },
		});
	});

	// Shift belongs to the press, not to the character: on a Thai keyboard it
	// produces a DIFFERENT Thai letter, so losing it types the wrong character
	// rather than the same one in lower case.
	it("keeps shift as part of the press", () => {
		expect(character(press("ศ", { code: "KeyL", shiftKey: true })).key).toEqual({
			code: "KeyL",
			shift: true,
		});
		expect(character(press("A", { code: "KeyA", shiftKey: true })).key).toEqual({
			code: "KeyA",
			shift: true,
		});
	});

	it("covers every position that carries a character", () => {
		const positions = [
			"KeyA",
			"KeyZ",
			"Digit0",
			"Digit9",
			"Minus",
			"Equal",
			"BracketLeft",
			"BracketRight",
			"Backslash",
			"Semicolon",
			"Quote",
			"Backquote",
			"Comma",
			"Period",
			"Slash",
			"Space",
		];
		for (const code of positions) {
			expect(character(press("x", { code })).key, code).toEqual({ code, shift: false });
		}
	});

	// ⚠ No position means no key to forward - the character still goes, by the
	// route the daemon plans for it. A synthetic event, an on-screen keyboard
	// and an accessibility tool all land here.
	it("is absent when the keystroke says nothing about which key it was", () => {
		for (const code of [undefined, "", "Unidentified", "F5", "IntlRo"]) {
			expect(character(press("a", code === undefined ? {} : { code })).key, String(code)).toBeUndefined();
		}
	});

	// ⚠ Caps Lock is the one case where the position and shift do NOT account
	// for the character: the Mac made a capital from an unshifted press, and the
	// device was never told about Caps Lock, so the same key would make the
	// lower-case letter there. Sending Shift instead would be a guess, and on a
	// layout where Caps Lock is not simply Shift it would be the wrong one.
	it("is absent while Caps Lock is what made the character", () => {
		expect(classifyKey(press("A", { code: "KeyA", capsLock: true }))).toEqual({ kind: "text", text: "A" });
	});
});
