import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useProjectSearchShortcut } from "./useProjectSearchShortcut";

function press(init: KeyboardEventInit): KeyboardEvent {
	const event = new KeyboardEvent("keydown", { key: "f", bubbles: true, cancelable: true, ...init });
	window.dispatchEvent(event);
	return event;
}

describe("useProjectSearchShortcut", () => {
	it("fires on ⌘⇧F", () => {
		const onRequest = vi.fn();
		renderHook(() => useProjectSearchShortcut(true, onRequest));
		const event = press({ metaKey: true, shiftKey: true });
		expect(onRequest).toHaveBeenCalledTimes(1);
		// The chord has to be swallowed: an unprevented ⌘⇧F reaches Monaco.
		expect(event.defaultPrevented).toBe(true);
	});

	it("fires on Ctrl+Shift+F, for the machines that are not Macs", () => {
		const onRequest = vi.fn();
		renderHook(() => useProjectSearchShortcut(true, onRequest));
		press({ ctrlKey: true, shiftKey: true });
		expect(onRequest).toHaveBeenCalledTimes(1);
	});

	it("fires again on a second press, so the box can be re-focused", () => {
		const onRequest = vi.fn();
		renderHook(() => useProjectSearchShortcut(true, onRequest));
		press({ metaKey: true, shiftKey: true });
		press({ metaKey: true, shiftKey: true });
		expect(onRequest).toHaveBeenCalledTimes(2);
	});

	it.each([
		["⌘F — Monaco's own find", { metaKey: true }],
		["⇧F — a capital letter", { shiftKey: true }],
		["⌥⌘⇧F — a superset that belongs to find-and-replace", { metaKey: true, shiftKey: true, altKey: true }],
		["a bare F", {}],
	])("does NOT fire on %s", (_label, init) => {
		const onRequest = vi.fn();
		renderHook(() => useProjectSearchShortcut(true, onRequest));
		const event = press(init);
		expect(onRequest).not.toHaveBeenCalled();
		expect(event.defaultPrevented).toBe(false);
	});

	it("does not fire on another letter", () => {
		const onRequest = vi.fn();
		renderHook(() => useProjectSearchShortcut(true, onRequest));
		window.dispatchEvent(new KeyboardEvent("keydown", { key: "b", metaKey: true, shiftKey: true }));
		expect(onRequest).not.toHaveBeenCalled();
	});

	it("is silent while disabled — an orchestrator has no worktree to search", () => {
		const onRequest = vi.fn();
		renderHook(() => useProjectSearchShortcut(false, onRequest));
		press({ metaKey: true, shiftKey: true });
		expect(onRequest).not.toHaveBeenCalled();
	});

	it("stops listening once unmounted", () => {
		const onRequest = vi.fn();
		const { unmount } = renderHook(() => useProjectSearchShortcut(true, onRequest));
		unmount();
		press({ metaKey: true, shiftKey: true });
		expect(onRequest).not.toHaveBeenCalled();
	});

	it("calls the LATEST callback without re-subscribing", () => {
		// SessionView rebuilds this callback every render; a listener rebound on
		// each one would be re-added and removed on every keystroke in the app.
		const first = vi.fn();
		const second = vi.fn();
		const { rerender } = renderHook(({ cb }) => useProjectSearchShortcut(true, cb), {
			initialProps: { cb: first },
		});
		rerender({ cb: second });
		press({ metaKey: true, shiftKey: true });
		expect(first).not.toHaveBeenCalled();
		expect(second).toHaveBeenCalledTimes(1);
	});
});
