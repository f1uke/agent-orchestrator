import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SimulatorPanel } from "./SimulatorPanel";

const { getMock, postMock, deleteMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	deleteMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock, DELETE: deleteMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") =>
		error instanceof Error ? error.message : ((error as { message?: string } | null)?.message ?? fallback),
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	subscribeApiBaseUrl: () => () => {},
}));

// A stand-in for the frame socket that records every open and close, because
// "was a socket open" is exactly "was a capture process running" on the daemon
// side - the whole CPU-safety story is visible here and nowhere else in jsdom.
type FakeSocket = { url: string; closed: boolean; binaryType: string; onmessage: ((e: MessageEvent) => void) | null };
const sockets: FakeSocket[] = [];

class MockWebSocket {
	static instances = sockets;
	url: string;
	closed = false;
	binaryType = "";
	onmessage: ((e: MessageEvent) => void) | null = null;
	onerror: (() => void) | null = null;
	onclose: (() => void) | null = null;
	constructor(url: string) {
		this.url = url;
		sockets.push(this as unknown as FakeSocket);
	}
	close() {
		this.closed = true;
	}
}

const openSockets = () => sockets.filter((s) => !s.closed);

// The wire the daemon writes: a kind byte, the framebuffer size, then the
// encoded bytes.
const KIND_DESCRIPTION = 1;
const KIND_KEYFRAME = 2;
const KIND_DELTA = 3;

function message(kind: number, payload: number[], width = 1320, height = 2868): ArrayBuffer {
	const out = new Uint8Array(5 + payload.length);
	const view = new DataView(out.buffer);
	view.setUint8(0, kind);
	view.setUint16(1, width);
	view.setUint16(3, height);
	out.set(payload, 5);
	return out.buffer;
}

// An avcC blob whose bytes 1..3 are the profile/constraints/level the codec
// string is built from - High profile, level 5.1, exactly what the device
// encoder emits.
const AVCC = [0x01, 0x64, 0x00, 0x33, 0xff, 0xe1];

type DecoderCall = { codec: string; description: unknown };
const decoderCalls: DecoderCall[] = [];
const decodedKinds: string[] = [];

class MockVideoDecoder {
	static instances: MockVideoDecoder[] = [];
	state = "unconfigured";
	output: (frame: { displayWidth: number; displayHeight: number; close: () => void }) => void;
	constructor(init: {
		output: (frame: { displayWidth: number; displayHeight: number; close: () => void }) => void;
		error: (err: Error) => void;
	}) {
		this.output = init.output;
		MockVideoDecoder.instances.push(this);
	}
	static refuseConfigure = false;
	configure(config: DecoderCall) {
		decoderCalls.push(config);
		if (MockVideoDecoder.refuseConfigure) throw new Error("this build will not decode avc1.640033");
		this.state = "configured";
	}
	decode(chunk: { type: string }) {
		decodedKinds.push(chunk.type);
		// A real decoder emits a frame; the panel only needs one to go live.
		this.output({ displayWidth: 1320, displayHeight: 2868, close: () => {} });
	}
	close() {
		this.state = "closed";
	}
}

const device = (overrides: Partial<Record<string, unknown>> = {}) => ({
	udid: "UDID-A",
	name: "iPhone 17 Pro Max",
	runtime: "iOS 26.3",
	runtimeIdentifier: "com.apple.CoreSimulator.SimRuntime.iOS-26-3",
	state: "Booted",
	available: true,
	default: false,
	lease: { state: "unknown", reason: "no AO session holds this device; AO cannot see whether a human is driving it" },
	...overrides,
});

function devicesPayload(devices: unknown[], defaultUdid: string | null, defaultReason: string) {
	return { data: { devices, defaultUdid, defaultReason }, error: undefined, response: { status: 200 } };
}

function wrapper({ children }: { children: ReactNode }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

/** Opens the panel's options menu, where the lease's own words live. */
async function openMenu() {
	await userEvent.click(await screen.findByRole("button", { name: /simulator and lease options/i }));
	return screen.findByRole("menu");
}

beforeEach(() => {
	sockets.length = 0;
	decoderCalls.length = 0;
	decodedKinds.length = 0;
	MockVideoDecoder.instances.length = 0;
	MockVideoDecoder.refuseConfigure = false;
	getMock.mockReset();
	postMock.mockReset().mockResolvedValue({ error: undefined });
	deleteMock.mockReset().mockResolvedValue({ error: undefined });
	vi.stubGlobal("WebSocket", MockWebSocket);
	vi.stubGlobal("VideoDecoder", MockVideoDecoder);
	vi.stubGlobal(
		"EncodedVideoChunk",
		class {
			type: string;
			constructor(init: { type: string }) {
				this.type = init.type;
			}
		},
	);
	vi.spyOn(document, "hasFocus").mockReturnValue(true);
	// jsdom has no 2d context; the panel only needs the call not to throw.
	HTMLCanvasElement.prototype.getContext = vi.fn().mockReturnValue({ drawImage: vi.fn() });
});

afterEach(() => {
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

describe("SimulatorPanel device selection", () => {
	it("says nothing is booted, and never offers to boot one", async () => {
		getMock.mockResolvedValue(devicesPayload([], null, "no simulator is booted"));
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		expect(await screen.findByText(/no simulator is booted/i)).toBeInTheDocument();
		expect(screen.getByText(/AO never boots, shuts down or erases one/i)).toBeInTheDocument();
		expect(openSockets()).toHaveLength(0);
	});

	// "No simulator is booted" is a claim about the machine. While nobody is
	// looking nothing has been asked, so making that claim would state something
	// AO never checked.
	it("does not claim there is no simulator when it has not looked", async () => {
		getMock.mockResolvedValue(devicesPayload([], null, "no simulator is booted"));
		render(<SimulatorPanel isActive={false} sessionId="p-1" />, { wrapper });

		expect(await screen.findByText(/Nothing is being captured while this window is not focused/i)).toBeInTheDocument();
		expect(screen.queryByText(/No simulator is booted/i)).not.toBeInTheDocument();
	});

	it("watches the one booted simulator without being asked", async () => {
		getMock.mockResolvedValue(devicesPayload([device()], "UDID-A", "the only booted simulator"));
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await waitFor(() => expect(openSockets()).toHaveLength(1));
		expect(openSockets()[0].url).toContain("/sim-stream/UDID-A");
	});

	// The refusal that matters: the CLI will not pick between two booted
	// simulators, and a picker that quietly picked one would be less honest than
	// the terminal.
	it("refuses to choose between two booted simulators and says why", async () => {
		getMock.mockResolvedValue(
			devicesPayload(
				[device(), device({ udid: "UDID-B", name: "iPhone 17 Pro" })],
				null,
				"2 simulators are booted, so there is no unambiguous default",
			),
		);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		expect(await screen.findByText(/2 simulators are booted/i)).toBeInTheDocument();
		expect(openSockets()).toHaveLength(0);
		expect(screen.getByText(/choose which booted simulator/i)).toBeInTheDocument();
	});
});

describe("SimulatorPanel layout", () => {
	beforeEach(() => {
		getMock.mockResolvedValue(devicesPayload([device()], "UDID-A", "the only booted simulator"));
	});

	// The complaint this rework answers: a picker, a freshness line, a lease row,
	// a checkbox and two paragraphs of guidance all competed with the screen.
	// Whatever else the toolbar grows, the screen has to be the thing that gets
	// the pane's remaining space.
	it("gives the screen every row the toolbar does not take", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		const canvas = await screen.findByTestId("sim-canvas");

		const stage = screen.getByTestId("sim-stage");
		expect(stage.className).toContain("flex-1");
		expect(stage.className).toContain("min-h-0");
		expect(stage).toContainElement(canvas);
		// The stage holds nothing but the screen: the device pill sits above it
		// and the control pills below, so every row the chrome does not take is
		// the screen's.
		expect(stage.querySelectorAll("button")).toHaveLength(0);
		// The box is fitted to the device's own shape, and object-contain is the
		// safety net for the frame before the stage has been measured.
		expect(canvas.className).toContain("object-contain");
		expect(canvas.className).toContain("h-full");
		expect(canvas.className).toContain("w-full");
	});

	// The panel offers no way to type. `ao sim type` and the daemon's type
	// gesture both still exist; what went is a field, a Send button and a
	// paragraph of keyboard caveat spent inside a pane whose whole point is the
	// screen. Driving a simulator by hand is tapping and swiping.
	it("offers no text field, and no paragraph explaining one", async () => {
		getMock.mockResolvedValue(
			devicesPayload([device({ lease: { state: "held", holder: "p-1" } })], "UDID-A", "the only booted simulator"),
		);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await userEvent.click(await screen.findByRole("button", { name: /drive this device/i }));

		expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /type into the focused field/i })).not.toBeInTheDocument();
		expect(screen.queryByText(/US-keyboard presses/i)).not.toBeInTheDocument();
		// The gestures a person actually reaches for are still one click away.
		expect(screen.getByRole("button", { name: /^home$/i })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /app switcher/i })).toBeInTheDocument();
	});
});

describe("SimulatorPanel capture lifetime", () => {
	beforeEach(() => {
		getMock.mockResolvedValue(devicesPayload([device()], "UDID-A", "the only booted simulator"));
	});

	it("captures nothing while the tab is not the one on screen", async () => {
		render(<SimulatorPanel isActive={false} sessionId="p-1" />, { wrapper });
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(openSockets()).toHaveLength(0);
	});

	it("stops capturing the moment the tab is hidden", async () => {
		const { rerender } = render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));

		rerender(<SimulatorPanel isActive={false} sessionId="p-1" />);
		await waitFor(() => expect(openSockets()).toHaveLength(0));
	});

	it("stops capturing when the window loses focus", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));

		vi.spyOn(document, "hasFocus").mockReturnValue(false);
		window.dispatchEvent(new Event("blur"));
		await waitFor(() => expect(openSockets()).toHaveLength(0));
	});

	it("stops capturing when the panel goes away entirely", async () => {
		const { unmount } = render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));

		unmount();
		await waitFor(() => expect(openSockets()).toHaveLength(0));
	});

	// The decoder holds a hardware session for as long as it is open, so a tab
	// that stops being looked at has to close it as well as the socket.
	it("closes the decoder as well as the socket when nobody is looking", async () => {
		const { rerender } = render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		expect(MockVideoDecoder.instances).toHaveLength(1);

		rerender(<SimulatorPanel isActive={false} sessionId="p-1" />);
		await waitFor(() => expect(MockVideoDecoder.instances[0].state).toBe("closed"));
	});

	// A device that disappears mid-view has to say so rather than leaving a stale
	// frame that reads as live.
	it("reports a stream that ended instead of showing a frozen screen as live", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));

		const socket = openSockets()[0];
		socket.onmessage?.({ data: JSON.stringify({ type: "ended", message: "the device is gone" }) } as MessageEvent);

		expect(await screen.findByText(/the device is gone/i)).toBeInTheDocument();
	});
});

describe("SimulatorPanel decoding", () => {
	beforeEach(() => {
		getMock.mockResolvedValue(devicesPayload([device()], "UDID-A", "the only booted simulator"));
	});

	// The codec string is not a constant: it is the profile and level the device
	// encoder actually chose, read back out of the parameter set it sent.
	it("configures the decoder from the parameter set the device sent", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));

		openSockets()[0].onmessage?.({ data: message(KIND_DESCRIPTION, AVCC) } as MessageEvent);
		expect(decoderCalls).toHaveLength(1);
		expect(decoderCalls[0].codec).toBe("avc1.640033");
	});

	// A delta that reaches a decoder with nothing configured fails the whole
	// stream rather than one frame, so it has to be dropped instead.
	it("decodes nothing before the decoder has a parameter set", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));

		openSockets()[0].onmessage?.({ data: message(KIND_DELTA, [0x41]) } as MessageEvent);
		expect(decodedKinds).toHaveLength(0);

		openSockets()[0].onmessage?.({ data: message(KIND_DESCRIPTION, AVCC) } as MessageEvent);
		openSockets()[0].onmessage?.({ data: message(KIND_KEYFRAME, [0x42]) } as MessageEvent);
		openSockets()[0].onmessage?.({ data: message(KIND_DELTA, [0x43]) } as MessageEvent);
		expect(decodedKinds).toEqual(["key", "delta"]);
	});

	// A daemon newer than this renderer could put a kind on the wire this build
	// has no meaning for. Decoding it as an ordinary frame would corrupt the
	// picture; ignoring it costs one frame.
	it("ignores a frame kind this build does not know", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		decodedKinds.length = 0;

		openSockets()[0].onmessage?.({ data: message(9, [0x44]) } as MessageEvent);
		expect(decodedKinds).toHaveLength(0);
	});

	// The decoder is dropped when it will not take a parameter set, because
	// everything after that set is encoded against it. Handing chunks to a
	// decoder that never configured fails the stream one frame at a time.
	it("stops decoding, and says why, when the parameter set is refused", async () => {
		MockVideoDecoder.refuseConfigure = true;
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));

		openSockets()[0].onmessage?.({ data: message(KIND_DESCRIPTION, AVCC) } as MessageEvent);
		openSockets()[0].onmessage?.({ data: message(KIND_KEYFRAME, [0x42]) } as MessageEvent);
		openSockets()[0].onmessage?.({ data: message(KIND_DELTA, [0x43]) } as MessageEvent);

		expect(decodedKinds).toHaveLength(0);
		expect(await screen.findByText(/will not decode/i)).toBeInTheDocument();
	});

	// A build without WebCodecs would otherwise show a black rectangle for ever.
	it("says so rather than showing a dead screen when this build cannot decode", async () => {
		vi.stubGlobal("VideoDecoder", undefined);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		expect(await screen.findByText(/cannot decode the simulator's video stream/i)).toBeInTheDocument();
		expect(openSockets()).toHaveLength(0);
	});
});

/** Gives the stream a complete starting point, which is what "live" means. */
function makeLive() {
	const socket = openSockets()[0];
	socket.onmessage?.({ data: message(KIND_DESCRIPTION, AVCC) } as MessageEvent);
	socket.onmessage?.({ data: message(KIND_KEYFRAME, [0x42]) } as MessageEvent);
}

describe("SimulatorPanel lease truth", () => {
	it("says unknown with the reason, never free, and offers to claim", async () => {
		getMock.mockResolvedValue(devicesPayload([device()], "UDID-A", "the only booted simulator"));
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		const menu = await openMenu();
		expect(within(menu).getByText(/Lease: unknown/i)).toBeInTheDocument();
		expect(within(menu).getByText(/cannot see whether a human is driving it/i)).toBeInTheDocument();
		expect(within(menu).queryByText(/free/i)).not.toBeInTheDocument();
		expect(within(menu).getByRole("menuitem", { name: /claim to drive/i })).toBeInTheDocument();
	});

	// The one place the lease is enforced in the UI is what is offered: a
	// session that does not hold the device is never given the control that
	// turns driving on, and the effect below switches it off if the lease moves.
	it("names the other holder and offers no way to drive", async () => {
		getMock.mockResolvedValue(
			devicesPayload([device({ lease: { state: "held", holder: "other-7" } })], "UDID-A", "the only booted simulator"),
		);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		expect(screen.queryByRole("button", { name: /drive this device/i })).not.toBeInTheDocument();
		const menu = await openMenu();
		expect(within(menu).getByText(/Leased by @other-7/i)).toBeInTheDocument();
		expect(within(menu).queryByRole("menuitem", { name: /claim to drive/i })).not.toBeInTheDocument();
	});

	it("offers driving only once this session holds the lease, and never pre-enabled", async () => {
		getMock.mockResolvedValue(
			devicesPayload([device({ lease: { state: "held", holder: "p-1" } })], "UDID-A", "the only booted simulator"),
		);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		const toggle = await screen.findByRole("button", { name: /drive this device/i });
		expect(toggle).toHaveAttribute("aria-pressed", "false");
	});
});

describe("SimulatorPanel driving", () => {
	const leased = () =>
		devicesPayload([device({ lease: { state: "held", holder: "p-1" } })], "UDID-A", "the only booted simulator");

	async function turnDrivingOn() {
		const toggle = await screen.findByRole("button", { name: /drive this device/i });
		await userEvent.click(toggle);
		return toggle;
	}

	/** Every gesture kind that reached the daemon, in order. */
	function gestureKinds(): string[] {
		return postMock.mock.calls
			.filter(([path]) => String(path).endsWith("/gesture"))
			.map(([, options]) => (options as { body?: { kind?: string } })?.body?.kind ?? "");
	}

	/** The y of every gesture of one kind that reached the daemon, in order. */
	function gesturePoints(kind: string): number[] {
		return postMock.mock.calls
			.filter(([path]) => String(path).endsWith("/gesture"))
			.map(([, options]) => (options as { body?: { kind?: string; y?: number } })?.body)
			.filter((body): body is { kind: string; y: number } => body?.kind === kind)
			.map((body) => body.y);
	}

	async function refresh() {
		const menu = await openMenu();
		await userEvent.click(within(menu).getByRole("menuitem", { name: /refresh simulators/i }));
	}

	beforeEach(() => {
		getMock.mockResolvedValue(leased());
	});

	it("sends nothing to the device while driving is off", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();

		const canvas = await screen.findByTestId("sim-canvas");
		await userEvent.pointer([{ target: canvas, keys: "[MouseLeft]" }]);
		expect(postMock).not.toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/sim-devices/{udid}/gesture",
			expect.anything(),
		);
	});

	// The one that matters for arbitration: a refusal from the daemon must reach
	// the human as words, not be swallowed into a screen that just did not react.
	it("surfaces a refusal when another session holds the device mid-gesture", async () => {
		postMock.mockImplementation(async (path: string) => {
			if (path.endsWith("/gesture")) {
				return { error: { message: "UDID-A is mid-gesture: another command holds the finger right now" } };
			}
			return { error: undefined };
		});
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();

		await userEvent.click(screen.getByRole("button", { name: /^home$/i }));
		expect(
			await screen.findByText("UDID-A is mid-gesture: another command holds the finger right now"),
		).toBeInTheDocument();
	});

	// Losing the lease mid-session has to take driving away with it. A toggle
	// left on for a device this session no longer owns is a click waiting to be
	// refused - or worse, to be aimed at whatever the new holder is doing.
	it("takes driving away the moment another session takes the lease", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();
		expect(screen.getByRole("button", { name: /^home$/i })).toBeInTheDocument();

		getMock.mockResolvedValue(
			devicesPayload([device({ lease: { state: "held", holder: "other-7" } })], "UDID-A", "the only booted simulator"),
		);
		await refresh();

		await waitFor(() => expect(screen.queryByRole("button", { name: /^home$/i })).not.toBeInTheDocument());
		expect(screen.queryByRole("button", { name: /drive this device/i })).not.toBeInTheDocument();
	});

	// The subtle one: a lease that goes away and comes back must not bring
	// driving back with it. Otherwise the toggle silently re-enables itself for a
	// device the human has not looked at since, and the next click lands blind.
	it("makes the human opt in again after the lease is lost and regained", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();
		expect(screen.getByRole("button", { name: /^home$/i })).toBeInTheDocument();

		getMock.mockResolvedValue(
			devicesPayload([device({ lease: { state: "held", holder: "other-7" } })], "UDID-A", "the only booted simulator"),
		);
		await refresh();
		await waitFor(() => expect(screen.queryByRole("button", { name: /^home$/i })).not.toBeInTheDocument());

		getMock.mockResolvedValue(leased());
		await refresh();

		const toggle = await screen.findByRole("button", { name: /drive this device/i });
		expect(toggle).toHaveAttribute("aria-pressed", "false");
		expect(screen.queryByRole("button", { name: /^home$/i })).not.toBeInTheDocument();
	});

	// The complaint this answers: a drag used to be replayed as one swipe after
	// the finger came up, so the screen started moving once the human had
	// stopped. It is now sent while the touch is still down.
	it("streams a drag while the finger is down instead of replaying it after", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();
		const canvas = await screen.findByTestId("sim-canvas");
		canvas.setPointerCapture = () => {};
		canvas.getBoundingClientRect = () => ({
			left: 0,
			top: 0,
			width: 200,
			height: 400,
			right: 200,
			bottom: 400,
			x: 0,
			y: 0,
			toJSON: () => ({}),
		});

		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300 });
		// Nothing may reach the device on the press alone: that is still a tap.
		expect(gestureKinds()).toEqual([]);

		fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 100, clientY: 200 });
		await waitFor(() => expect(gestureKinds()).toContain("drag-begin"));

		// The finger keeps going, and every step of it keeps reaching the device -
		// one move is not "following", it is the drag opening.
		for (const y of [180, 160, 140, 120]) {
			fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 100, clientY: y });
			await waitFor(() => expect(gesturePoints("drag-move").at(-1)).toBeCloseTo(y / 400, 5));
		}
		expect(gestureKinds().filter((k) => k === "drag-move").length).toBeGreaterThanOrEqual(4);

		fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 100, clientY: 120 });
		await waitFor(() => expect(gestureKinds()).toContain("drag-end"));

		// And never as one swipe after the fact.
		expect(gestureKinds()).not.toContain("swipe");
	});

	// A press that does not move is still a tap, which holds the finger down for
	// a measured moment a drag's begin does not.
	it("still sends a tap for a press that does not move", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();
		const canvas = await screen.findByTestId("sim-canvas");
		canvas.setPointerCapture = () => {};
		canvas.getBoundingClientRect = () => ({
			left: 0,
			top: 0,
			width: 200,
			height: 400,
			right: 200,
			bottom: 400,
			x: 0,
			y: 0,
			toJSON: () => ({}),
		});

		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300 });
		fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 102, clientY: 301 });
		fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 102, clientY: 301 });

		await waitFor(() => expect(gestureKinds()).toEqual(["tap"]));
	});

	it("sends a home press through the arbitrated gesture route", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();

		await userEvent.click(screen.getByRole("button", { name: /^home$/i }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/sim-devices/{udid}/gesture",
				expect.objectContaining({ body: { kind: "button", name: "home" } }),
			),
		);
	});
});
