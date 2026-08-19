import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
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

describe("typing into the device", () => {
	// 🗝 The reason this slice exists. A character used to wait for the human to
	// pause typing (250 ms) and then for the daemon to ask the guest which
	// keyboard it had (~935 ms), so the first character of "hello" reached the
	// device 1738 ms after it was pressed and all five arrived at once. On a
	// guest that reads US ASCII faithfully each one now goes out on its own,
	// which is a 3-6 ms round trip.
	it("sends each character on its own, without waiting for a pause", async () => {
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();
		await waitFor(() => expect(getMock).toHaveBeenCalledWith("/api/v1/sim/devices/{udid}/keyboard", expect.anything()));

		canvas().focus();
		await userEvent.keyboard("hello");
		// Deliberately NOT settled first: nothing here may be waiting on a timer.
		await waitFor(() => expect(sent()).toHaveLength(5));

		expect(sent()).toEqual([
			{ kind: "type", text: "h" },
			{ kind: "type", text: "e" },
			{ kind: "type", text: "l" },
			{ kind: "type", text: "l" },
			{ kind: "type", text: "o" },
		]);
		expect(postMock).toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/sim-devices/{udid}/gesture",
			expect.objectContaining({ params: { path: { sessionId: "p-1", udid: UDID } } }),
		);
	});

	// 🗝 The case the whole design is for. The browser resolved the input
	// source, so the pane never has to guess a character from a key code -
	// which is what made `ao sim type "fa12345"` arrive as Thai gibberish.
	it("sends Thai exactly as typed", async () => {
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		canvas().focus();
		await userEvent.keyboard("สวัสดี");
		await settle();

		expect(sent()).toEqual([{ kind: "type", text: "สวัสดี" }]);
	});

	// ⚠ Still batched where batching is what makes it correct. Each `type` on a
	// remapping guest is a pasteboard round trip that reads the screen twice to
	// prove it landed, measured at 3.1-3.4 s - one per character would be far
	// worse than the pause this slice removed, and would cycle the guest's
	// pasteboard on every keystroke.
	it("batches a burst on a guest that would remap the keys", async () => {
		serve(heldByUs, remappingGuest);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		canvas().focus();
		await userEvent.keyboard("ab");
		await settle();
		await userEvent.keyboard("cd");
		await settle();

		expect(sent()).toEqual([
			{ kind: "type", text: "ab" },
			{ kind: "type", text: "cd" },
		]);
	});

	// ⚠ Ordering is the property that must not break: a backspace that arrived
	// after the text it was meant to delete would silently corrupt what was
	// typed - into a password field, invisibly.
	it("flushes pending text before a key, so editing arrives in order", async () => {
		serve(heldByUs, remappingGuest);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		canvas().focus();
		await userEvent.keyboard("ab{Backspace}c");
		await settle();

		expect(sent()).toEqual([
			{ kind: "type", text: "ab" },
			{ kind: "key", name: "backspace" },
			{ kind: "type", text: "c" },
		]);
	});

	// ⚠ Even on a US guest, a character no US key can send has to go through the
	// pasteboard - so Thai is batched by the TEXT, whatever the input mode says.
	// This is the property that makes a remembered input mode safe to reuse at
	// all: a human who switches their Mac to Thai starts producing Thai runes,
	// and those are routed by what they are rather than by what was remembered.
	it("batches characters no US key can send, even on a US guest", async () => {
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();
		await waitFor(() => expect(getMock).toHaveBeenCalledWith("/api/v1/sim/devices/{udid}/keyboard", expect.anything()));

		canvas().focus();
		await userEvent.keyboard("สวัสดี");
		await settle();

		expect(sent()).toEqual([{ kind: "type", text: "สวัสดี" }]);
	});

	it("sends Enter, Tab and the arrows as keys rather than as text", async () => {
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		canvas().focus();
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

		canvas().focus();
		await userEvent.keyboard("half");
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

		canvas().focus();
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

		canvas().focus();
		await userEvent.keyboard("สวัสดี");
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

		canvas().focus();
		await userEvent.keyboard("สวัสดี");
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

		canvas().focus();
		await userEvent.keyboard("hello");
		await settle();

		expect(sent()).toEqual([]);
	});

	it("sends nothing when this session does not hold the lease", async () => {
		serve(heldByNobody);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await screen.findByRole("button", { name: /claim to drive/i });

		canvas().focus();
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
		canvas().focus();
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

		canvas().focus();
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
