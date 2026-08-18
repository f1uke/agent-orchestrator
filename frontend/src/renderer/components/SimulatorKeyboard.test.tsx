import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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

function serve(lease: Record<string, unknown>) {
	getMock.mockImplementation(async (path: string) => {
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
	vi.spyOn(document, "hasFocus").mockReturnValue(true);
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
	it("sends what was typed as text, through the same gesture route as a tap", async () => {
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		canvas().focus();
		await userEvent.keyboard("hello");
		await settle();

		expect(sent()).toEqual([{ kind: "type", text: "hello" }]);
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

	it("batches a burst into one request and starts a new one after a pause", async () => {
		serve(heldByUs);
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
		serve(heldByUs);
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
		serve(heldByUs);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await drive();

		canvas().focus();
		await userEvent.keyboard("half");
		canvas().blur();
		await vi.advanceTimersByTimeAsync(0);

		expect(sent()).toEqual([{ kind: "type", text: "half" }]);
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
