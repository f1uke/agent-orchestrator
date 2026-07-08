import { afterEach, describe, expect, it, vi } from "vitest";
import { focusTerminal, registerTerminalFocus, returnFocusToTerminal } from "./terminal-focus";

// Keep module state clean between tests: whatever a test registers, it also
// unregisters via the returned disposer.
afterEach(() => {
	// focusTerminal on nothing is a no-op; this documents the reset expectation.
});

describe("terminal-focus registry", () => {
	it("is a no-op returning false when no terminal is registered", () => {
		expect(focusTerminal()).toBe(false);
	});

	it("focuses the registered terminal and reports that it did", () => {
		const focus = vi.fn();
		const unregister = registerTerminalFocus(focus);

		expect(focusTerminal()).toBe(true);
		expect(focus).toHaveBeenCalledTimes(1);

		unregister();
		expect(focusTerminal()).toBe(false);
		expect(focus).toHaveBeenCalledTimes(1);
	});

	it("uses the most recently registered terminal (the active pane)", () => {
		const first = vi.fn();
		const second = vi.fn();
		const unregisterFirst = registerTerminalFocus(first);
		const unregisterSecond = registerTerminalFocus(second);

		focusTerminal();
		expect(first).not.toHaveBeenCalled();
		expect(second).toHaveBeenCalledTimes(1);

		// A stale unregister from the older pane must not clear the active one.
		unregisterFirst();
		expect(focusTerminal()).toBe(true);
		expect(second).toHaveBeenCalledTimes(2);

		unregisterSecond();
		expect(focusTerminal()).toBe(false);
	});
});

describe("returnFocusToTerminal (overlay onCloseAutoFocus helper)", () => {
	it("focuses the terminal and prevents the framework's default focus return", () => {
		const focus = vi.fn();
		const unregister = registerTerminalFocus(focus);
		const event = { preventDefault: vi.fn() };

		returnFocusToTerminal(event);

		expect(focus).toHaveBeenCalledTimes(1);
		expect(event.preventDefault).toHaveBeenCalledTimes(1);
		unregister();
	});

	it("leaves the default focus return alone when no terminal is mounted", () => {
		const event = { preventDefault: vi.fn() };

		returnFocusToTerminal(event);

		expect(event.preventDefault).not.toHaveBeenCalled();
	});
});
