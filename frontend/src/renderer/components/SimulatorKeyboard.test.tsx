import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TYPING_FLUSH_MS, TYPING_WAIT_VISIBLE_MS } from "../hooks/useDeviceKeyboard";
import { SimulatorPanel } from "./SimulatorPanel";

const { getMock, postMock, deleteMock, patchMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	deleteMock: vi.fn(),
	patchMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock, DELETE: deleteMock, PATCH: patchMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") =>
		error instanceof Error ? error.message : ((error as { message?: string } | null)?.message ?? fallback),
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	subscribeApiBaseUrl: () => () => {},
}));

class MockWebSocket {
	closed = false;
	binaryType = "";
	onmessage: ((e: MessageEvent) => void) | null = null;
	onerror: (() => void) | null = null;
	onclose: (() => void) | null = null;
	constructor(public url: string) {}
	close() {
		this.closed = true;
	}
}

// The pane refuses to drive a stream it cannot decode, so a decoder has to
// exist for any input - pointer or keyboard - to be allowed through at all.
class MockVideoDecoder {
	state = "unconfigured";
	constructor(_init: unknown) {}
	configure() {
		this.state = "configured";
	}
	decode() {}
	close() {
		this.state = "closed";
	}
}

const UDID = "UDID-A";
const heldByUs = { state: "held", holder: "p-1" };
const heldByNobody = { state: "unknown", reason: "no AO session holds this device" };

/**
 * A guest that reads US ASCII key presses as the characters they were sent as.
 * That is what lets the pane send a character on its own; see `usGuest` vs
 * `remappingGuest` below.
 */
const usGuest = { udid: UDID, mode: "US", sendsUSASCII: true };
/** A guest that would turn those key presses into other characters. */
const remappingGuest = {
	udid: UDID,
	mode: "Thai",
	sendsUSASCII: false,
	reason: "the simulator's keyboard input mode is Thai, which would remap the key presses",
};

function serve(lease: Record<string, unknown>, keyboard: Record<string, unknown> = usGuest) {
	getMock.mockImplementation(async (path: string) => {
		if (path.includes("/keyboard")) {
			return { data: keyboard, error: undefined, response: { status: 200 } };
		}
		if (path === "/api/v1/sim/devices") {
			return {
				data: {
					devices: [
						{
							udid: UDID,
							name: "iPhone 17 Pro Max",
							runtime: "iOS 26.3",
							runtimeIdentifier: "com.apple.CoreSimulator.SimRuntime.iOS-26-3",
							state: "Booted",
							available: true,
							default: true,
							lease,
						},
					],
					defaultUdid: UDID,
					defaultReason: "the only booted simulator",
				},
				error: undefined,
				response: { status: 200 },
			};
		}
		if (path.includes("sim-recordings")) {
			return { data: undefined, error: { message: "not found" }, response: { status: 404 } };
		}
		if (path.includes("sim-flows")) return { data: { flows: [] }, error: undefined, response: { status: 200 } };
		throw new Error(`unexpected GET ${path}`);
	});
}

function wrapper({ children }: { children: ReactNode }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const canvas = () => screen.getByTestId("sim-canvas");

/** Every gesture body the pane sent, in order. */
function sent(): Record<string, unknown>[] {
	return postMock.mock.calls
		.filter(([path]) => String(path).includes("/gesture"))
		.map(([, options]) => (options as { body: Record<string, unknown> }).body);
}

/** Turn driving on: it is what makes any input reach the device. */
async function drive() {
	const button = await screen.findByRole("button", { name: /drive this device/i });
	if (button.getAttribute("aria-pressed") !== "true") await userEvent.click(button);
	await waitFor(() => expect(button).toHaveAttribute("aria-pressed", "true"));
}

beforeEach(() => {
	vi.useFakeTimers({ shouldAdvanceTime: true });
	getMock.mockReset();
	postMock.mockReset().mockResolvedValue({ error: undefined });
	deleteMock.mockReset().mockResolvedValue({ error: undefined });
	patchMock.mockReset().mockResolvedValue({ error: undefined });
	vi.stubGlobal("WebSocket", MockWebSocket);
	vi.stubGlobal("VideoDecoder", MockVideoDecoder);
	vi.stubGlobal(
		"EncodedVideoChunk",
		class {
			constructor(public init: { type: string }) {}
		},
	);
	sessionStorage.clear();
	HTMLCanvasElement.prototype.getContext = vi.fn().mockReturnValue({ drawImage: vi.fn() });
});

afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

/** Let a burst of typing settle into its request. */
async function settle() {
	await vi.advanceTimersByTimeAsync(400);
}

/**
 * A keystroke as a REAL keyboard delivers it: the character the layout produced
 * AND the position of the key that produced it.
 *
 * ⚠ `userEvent.keyboard` cannot express this. Its map knows the US layout, so
 * it can pair `a` with `KeyA` but has no position at all for `ส` - which is
 * precisely the pairing that matters here, and the reason this dispatches the
 * event itself. jsdom then tells us what the pane SENT; whether that reached a
 * device is a question only a device can answer, and the record says which
 * claims rest on that.
 */
function typeOn(key: string, code: string, init: KeyboardEventInit = {}) {
	fireEvent.keyDown(canvas(), { key, code, ...init });
}

/**
 * Focus the device surface and let React see it.
 *
 * ⚠ Typing reaches the device only while the surface HAS focus, and that is
 * React state - so a keystroke dispatched in the same tick as the focus sees
 * the pane still switched off. `userEvent` flushes on its own; a dispatched
 * event does not.
 */
async function focusCanvas() {
	canvas().focus();
	await vi.advanceTimersByTimeAsync(0);
}

describe("typing into the device", () => {
	// 🗝 The reason this slice exists. A character used to wait for the human to
	// pause typing (250 ms) and then for the daemon to ask the guest which
	// keyboard it had (~935 ms), so the first character of "hello" reached the
	// device 1738 ms after it was pressed and all five arrived at once. Now each
	// keystroke goes out on its own, carrying the position of the key that
	// produced it - a 1-2 ms round trip on the device.
	it("sends each character on its own, without waiting for a pause", async () => {
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();
		await waitFor(() => expect(getMock).toHaveBeenCalledWith("/api/v1/sim/devices/{udid}/keyboard", expect.anything()));

		await focusCanvas();
		await userEvent.keyboard("hi");
		// Deliberately NOT settled first: nothing here may be waiting on a timer.
		await waitFor(() => expect(sent()).toHaveLength(2));

		expect(sent()).toEqual([
			{ kind: "type", text: "h", keys: [{ code: "KeyH", shift: false }] },
			{ kind: "type", text: "i", keys: [{ code: "KeyI", shift: false }] },
		]);
		expect(postMock).toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/sim-devices/{udid}/gesture",
			expect.objectContaining({ params: { path: { sessionId: "p-1", udid: UDID } } }),
		);
	});

	// 🗝 #277, at the pane. The keys the human pressed still travel - they are
	// what lets the daemon send a real key press wherever the guest reads it as
	// the character that was typed - but having a position is no longer a
	// reason to send a keystroke on its own. This guest would remap those
	// positions, so the daemon has to deliver the CHARACTERS, and one
	// pasteboard round trip per BURST is the only bearable way to do that.
	// Sending them one at a time bought a 2.7-3.7 s trip per keystroke.
	it("sends a Thai burst as one request, carrying the keys that produced it", async () => {
		serve(heldByUs, remappingGuest);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		await focusCanvas();
		// "สว" on a Thai Mac: the keys a US keyboard prints `l` and `;` on.
		typeOn("ส", "KeyL");
		typeOn("ว", "Semicolon");
		await settle();

		expect(sent()).toEqual([
			{
				kind: "type",
				text: "สว",
				keys: [
					{ code: "KeyL", shift: false },
					{ code: "Semicolon", shift: false },
				],
			},
		]);
	});

	// ⚠ Shift belongs to the KEY, not to the character: it is part of what was
	// pressed, so the daemon needs it to work out what the guest would read.
	it("carries shift with the key it was held for", async () => {
		serve(heldByUs, remappingGuest);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		await focusCanvas();
		typeOn("ศ", "KeyL", { shiftKey: true });
		await settle();

		expect(sent()).toEqual([{ kind: "type", text: "ศ", keys: [{ code: "KeyL", shift: true }] }]);
	});

	// ⚠ Caps Lock is reported, not judged (#277). The pane used to drop the
	// position here, on the grounds that an unshifted press cannot account for
	// a capital - true, and not enough: on a Mac that uses Caps Lock to SWITCH
	// INPUT SOURCE the modifier state is never set, so a Thai keystroke looked
	// exactly like a US one. The daemon compares the character against what
	// the guest would read from the position, which catches both.
	it("reports the position even when Caps Lock made the character", async () => {
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		await focusCanvas();
		typeOn("A", "KeyA", { modifierCapsLock: true });
		await settle();

		expect(sent()).toEqual([{ kind: "type", text: "A", keys: [{ code: "KeyA", shift: false }] }]);
	});

	// ⚠ Still batched where there is no position to forward, because there the
	// daemon has to plan the route: on a guest that would remap the keys that is
	// a pasteboard round trip which reads the screen twice to prove it landed,
	// measured at 2.7-3.7 s. One per keystroke would be far worse than the pause
	// it replaced, and would cycle the guest's pasteboard on every character.
	it("batches a burst of keystrokes that carry no position", async () => {
		serve(heldByUs, remappingGuest);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		await focusCanvas();
		// An on-screen keyboard, an accessibility tool, a synthetic event: the
		// character is known and the key that made it is not.
		typeOn("a", "");
		typeOn("b", "");
		await settle();
		typeOn("c", "");
		typeOn("d", "");
		await settle();

		expect(sent()).toEqual([
			{ kind: "type", text: "ab" },
			{ kind: "type", text: "cd" },
		]);
	});

	// A burst that has already lost its positions cannot regain them: the keys
	// have to account for every character or for none, so one forwardable key
	// mixed into it goes by the slower route WITH the rest rather than jumping
	// ahead of what was typed before it.
	it("keeps a burst in order rather than letting one forwardable key overtake it", async () => {
		serve(heldByUs, remappingGuest);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		await focusCanvas();
		typeOn("a", "");
		typeOn("b", "KeyB");
		await settle();

		expect(sent()).toEqual([{ kind: "type", text: "ab" }]);
	});

	// ⚠ Ordering is the property that must not break: a backspace that arrived
	// after the text it was meant to delete would silently corrupt what was
	// typed - into a password field, invisibly.
	it("flushes pending text before a key, so editing arrives in order", async () => {
		serve(heldByUs, remappingGuest);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		await focusCanvas();
		typeOn("a", "");
		typeOn("b", "");
		await userEvent.keyboard("{Backspace}");
		typeOn("c", "");
		await settle();

		expect(sent()).toEqual([
			{ kind: "type", text: "ab" },
			{ kind: "key", name: "backspace" },
			{ kind: "type", text: "c" },
		]);
	});

	// ⚠ Even on a US guest, a character no US key can send and no position to
	// forward has to go through the pasteboard - so it is batched by the TEXT,
	// whatever the input mode says. This is the property that makes a remembered
	// input mode safe to reuse at all: what a keystroke IS decides its route,
	// not what was remembered about the guest.
	it("batches characters no US key can send, even on a US guest", async () => {
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();
		await waitFor(() => expect(getMock).toHaveBeenCalledWith("/api/v1/sim/devices/{udid}/keyboard", expect.anything()));

		await focusCanvas();
		for (const rune of "สวัสดี") typeOn(rune, "");
		await settle();

		expect(sent()).toEqual([{ kind: "type", text: "สวัสดี" }]);
	});

	// 🗝 The human's own configuration in #277: a Thai Mac, a guest still on
	// en_US. Every keystroke carries a position AND a Thai character, and the
	// positions are the ones this guest would read as "fa" - so they cannot be
	// sent as themselves, the characters go through the pasteboard, and the
	// burst is what makes that bearable. Before the fix this was six separate
	// requests, each answered "1 key press forwarded", each putting a letter
	// nobody typed into the field.
	it("batches a Thai burst typed on a Mac whose guest is still on US", async () => {
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();
		await waitFor(() => expect(getMock).toHaveBeenCalledWith("/api/v1/sim/devices/{udid}/keyboard", expect.anything()));

		await focusCanvas();
		typeOn("ด", "KeyF");
		typeOn("ฟ", "KeyA");
		await settle();

		expect(sent()).toEqual([
			{
				kind: "type",
				text: "ดฟ",
				keys: [
					{ code: "KeyF", shift: false },
					{ code: "KeyA", shift: false },
				],
			},
		]);
	});

	it("sends Enter, Tab and the arrows as keys rather than as text", async () => {
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		await focusCanvas();
		await userEvent.keyboard("{Enter}{Tab}{ArrowUp}{ArrowDown}{ArrowLeft}{ArrowRight}");
		await settle();

		expect(sent()).toEqual([
			{ kind: "key", name: "enter" },
			{ kind: "key", name: "tab" },
			{ kind: "key", name: "arrow-up" },
			{ kind: "key", name: "arrow-down" },
			{ kind: "key", name: "arrow-left" },
			{ kind: "key", name: "arrow-right" },
		]);
	});

	it("does not strand what was typed when focus leaves", async () => {
		serve(heldByUs, remappingGuest);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		await focusCanvas();
		for (const rune of "half") typeOn(rune, "");
		canvas().blur();
		await vi.advanceTimersByTimeAsync(0);

		expect(sent()).toEqual([{ kind: "type", text: "half" }]);
	});
});

/**
 * The pane admits when something typed has not arrived yet.
 *
 * 🗝 Why this is not decoration. Where the text has to go through the guest's
 * pasteboard it is still batched, and in that gap the human sees nothing
 * happen - so they retype, or tap around thinking the field lost focus, and a
 * correct system still ends up with the wrong thing in the field. What it must
 * never do is show what was typed.
 */
describe("saying that typing is still on its way", () => {
	const waitingSlot = () => screen.getByTestId("sim-typing-waiting");

	it("says nothing while typing is landing promptly", async () => {
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();
		await waitFor(() => expect(getMock).toHaveBeenCalledWith("/api/v1/sim/devices/{udid}/keyboard", expect.anything()));

		await focusCanvas();
		await userEvent.keyboard("ab");
		await waitFor(() => expect(sent()).toHaveLength(2));
		await vi.advanceTimersByTimeAsync(400);

		expect(waitingSlot()).toBeEmptyDOMElement();
	});

	it("says so once text has been on its way long enough to doubt it", async () => {
		serve(heldByUs, remappingGuest);
		let land: () => void = () => {};
		postMock.mockImplementation(
			async () =>
				await new Promise((resolve) => {
					land = () => resolve({ error: undefined });
				}),
		);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		await focusCanvas();
		for (const rune of "สวัสดี") typeOn(rune, "");
		await vi.advanceTimersByTimeAsync(TYPING_FLUSH_MS + TYPING_WAIT_VISIBLE_MS + 50);

		expect(waitingSlot()).not.toBeEmptyDOMElement();
		// ⚠ Never the text itself: it is written verbatim into a recorded flow
		// and is a password often enough that it must not reach the DOM.
		expect(waitingSlot().textContent ?? "").not.toContain("สวัสดี");
		expect(document.body.textContent ?? "").not.toContain("สวัสดี");

		land();
		await waitFor(() => expect(waitingSlot()).toBeEmptyDOMElement());
	});

	// A send that failed is not still on its way. Leaving it saying so would be
	// a spinner that never stops, on the one surface where a human is deciding
	// whether to type the whole thing again.
	it("stops saying so when the text could not be delivered", async () => {
		serve(heldByUs, remappingGuest);
		postMock.mockResolvedValue({ error: { message: "nope" } });
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		await focusCanvas();
		for (const rune of "สวัสดี") typeOn(rune, "");
		await vi.advanceTimersByTimeAsync(TYPING_FLUSH_MS + TYPING_WAIT_VISIBLE_MS + 50);

		await waitFor(() => expect(waitingSlot()).toBeEmptyDOMElement());
	});
});

describe("the keyboard is not stolen from the rest of the app", () => {
	it("sends nothing while the device surface does not have focus", async () => {
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		// Typing with focus anywhere else in the app.
		await userEvent.keyboard("hello");
		await settle();

		expect(sent()).toEqual([]);
	});

	it("sends nothing while driving is off, even with the surface focused", async () => {
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await screen.findByRole("button", { name: /drive this device/i });

		await focusCanvas();
		await userEvent.keyboard("hello");
		await settle();

		expect(sent()).toEqual([]);
	});

	it("sends nothing when this session does not hold the lease", async () => {
		serve(heldByNobody);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await screen.findByRole("button", { name: /claim to drive/i });

		await focusCanvas();
		await userEvent.keyboard("hello");
		await settle();

		expect(sent()).toEqual([]);
	});

	// ⚠ A tab that swallowed ⌘W would be a worse bug than the one being fixed.
	it("lets shortcuts through even while the surface is focused", async () => {
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		const seen: string[] = [];
		const listener = (event: KeyboardEvent) => {
			if (!event.defaultPrevented) seen.push(event.key);
		};
		document.addEventListener("keydown", listener);
		await focusCanvas();
		await userEvent.keyboard("{Meta>}w{/Meta}{Control>}k{/Control}{Alt>}f{/Alt}");
		await settle();
		document.removeEventListener("keydown", listener);

		expect(sent()).toEqual([]);
		// Not merely unsent - not swallowed either, so whatever binds them still
		// fires. (The bare modifier presses are in there too; what matters is
		// that the shortcut keys themselves came through unprevented.)
		expect(seen).toContain("w");
		expect(seen).toContain("k");
		expect(seen).toContain("f");
	});

	// The way out has to exist and has to be obvious, or a keyboard user is
	// trapped on a surface that eats Tab.
	it("Escape leaves the surface and is never sent to the device", async () => {
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		await focusCanvas();
		expect(canvas()).toHaveFocus();
		await userEvent.keyboard("{Escape}");
		await settle();

		expect(canvas()).not.toHaveFocus();
		expect(sent()).toEqual([]);
	});

	it("is not in the tab order at all while a touch cannot reach the device", async () => {
		serve(heldByNobody);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await screen.findByRole("button", { name: /claim to drive/i });

		expect(canvas()).toHaveAttribute("tabindex", "-1");
	});
});
