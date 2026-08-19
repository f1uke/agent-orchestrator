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

/**
 * The device list is not the only thing this panel asks for - it also reads
 * the device's recording and this session's recorded flows - so the client has
 * to answer by ROUTE.
 *
 * It used to answer every GET with the device list, which meant the recording
 * query got a body with no recording in it and the flows query got one with no
 * flows. Both are the panel's own queries failing, and one of them managed to
 * make an unrelated case in this file flake on CI while passing locally. Those
 * two surfaces have their own tests in SimRecordControls.test.tsx; here they
 * only have to answer plausibly and stay out of the way.
 */
function serveDevices(payload: ReturnType<typeof devicesPayload>) {
	getMock.mockImplementation(async (path: string) => {
		if (path.includes("sim-recordings")) {
			return { data: undefined, error: { message: "not found" }, response: { status: 404 } };
		}
		if (path.includes("sim-flows")) {
			return { data: { flows: [] }, error: undefined, response: { status: 200 } };
		}
		return payload;
	});
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
	sessionStorage.clear();
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
		serveDevices(devicesPayload([], null, "no simulator is booted"));
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		expect(await screen.findByText(/no simulator is booted/i)).toBeInTheDocument();
		expect(screen.getByText(/AO never boots, shuts down or erases one/i)).toBeInTheDocument();
		expect(openSockets()).toHaveLength(0);
	});

	// "No simulator is booted" is a claim about the machine. While nobody is
	// looking nothing has been asked, so making that claim would state something
	// AO never checked.
	it("does not claim there is no simulator when it has not looked", async () => {
		serveDevices(devicesPayload([], null, "no simulator is booted"));
		render(<SimulatorPanel isActive={false} sessionId="p-1" />, { wrapper });

		expect(await screen.findByText(/Nothing is being captured while this window is not focused/i)).toBeInTheDocument();
		expect(screen.queryByText(/No simulator is booted/i)).not.toBeInTheDocument();
	});

	it("watches the one booted simulator without being asked", async () => {
		serveDevices(devicesPayload([device()], "UDID-A", "the only booted simulator"));
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await waitFor(() => expect(openSockets()).toHaveLength(1));
		expect(openSockets()[0].url).toContain("/sim-stream/UDID-A");
	});

	// The refusal that matters: the CLI will not pick between two booted
	// simulators, and a picker that quietly picked one would be less honest than
	// the terminal.
	it("refuses to choose between two booted simulators and says why", async () => {
		serveDevices(
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

describe("SimulatorPanel remembering a worker", () => {
	const leased = () =>
		devicesPayload([device({ lease: { state: "held", holder: "p-1" } })], "UDID-A", "the only booted simulator");

	// Switching to another worker and back remounts this panel. Picking the
	// device again and opting in to driving again every time was the complaint.
	it("comes back to the device and the driving it was left with", async () => {
		serveDevices(leased());
		const first = render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await userEvent.click(await screen.findByRole("button", { name: /drive this device/i }));
		first.unmount();

		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		expect(openSockets()[0].url).toContain("/sim-stream/UDID-A");
		const toggle = await screen.findByRole("button", { name: /drive this device/i });
		await waitFor(() => expect(toggle).toHaveAttribute("aria-pressed", "true"));
	});

	// What comes back is a device this session still owns. Remembering that
	// driving was on is not the same as still being allowed to drive.
	it("does not hand driving back when the lease has moved on", async () => {
		serveDevices(leased());
		const first = render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await userEvent.click(await screen.findByRole("button", { name: /drive this device/i }));
		first.unmount();

		serveDevices(
			devicesPayload([device({ lease: { state: "held", holder: "other-7" } })], "UDID-A", "the only booted simulator"),
		);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await waitFor(() => expect(screen.getByRole("button", { name: /^home$/i })).toBeDisabled());
		expect(screen.queryByRole("button", { name: /drive this device/i })).not.toBeInTheDocument();
	});

	// One worker's choice is not another's.
	it("keeps each worker's device separate", async () => {
		serveDevices(
			devicesPayload(
				[device(), device({ udid: "UDID-B", name: "iPhone 17 Pro" })],
				null,
				"2 simulators are booted, so there is no unambiguous default",
			),
		);
		const first = render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await screen.findByText(/2 simulators are booted/i);
		await userEvent.click(screen.getByRole("combobox", { name: /simulator to watch/i }));
		await userEvent.click(await screen.findByRole("option", { name: /iPhone 17 Pro ·/i }));
		await waitFor(() => expect(openSockets()[0]?.url).toContain("/sim-stream/UDID-B"));
		first.unmount();

		// A different worker starts where it always did: with the refusal.
		render(<SimulatorPanel isActive sessionId="p-2" />, { wrapper });
		expect(await screen.findByText(/2 simulators are booted/i)).toBeInTheDocument();
		expect(openSockets()).toHaveLength(0);
	});
});

describe("SimulatorPanel layout", () => {
	beforeEach(() => {
		serveDevices(devicesPayload([device()], "UDID-A", "the only booted simulator"));
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
		serveDevices(
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
		serveDevices(devicesPayload([device()], "UDID-A", "the only booted simulator"));
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
		serveDevices(devicesPayload([device()], "UDID-A", "the only booted simulator"));
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
/** Ask for the device list again, the way the menu item does. */
async function refresh() {
	const menu = await openMenu();
	await userEvent.click(within(menu).getByRole("menuitem", { name: /refresh simulators/i }));
}

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

/**
 * jsdom lays nothing out, so a canvas has no box and every pointer event would
 * land outside the screen. This gives it one.
 */
function stubCanvasBox(canvas: HTMLElement) {
	(canvas as HTMLElement & { setPointerCapture: () => void }).setPointerCapture = () => {};
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
}

function makeLive() {
	const socket = openSockets()[0];
	socket.onmessage?.({ data: message(KIND_DESCRIPTION, AVCC) } as MessageEvent);
	socket.onmessage?.({ data: message(KIND_KEYFRAME, [0x42]) } as MessageEvent);
}

describe("SimulatorPanel lease truth", () => {
	it("says unknown with the reason, never free", async () => {
		serveDevices(devicesPayload([device()], "UDID-A", "the only booted simulator"));
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		const menu = await openMenu();
		expect(within(menu).getByText(/Lease: unknown/i)).toBeInTheDocument();
		expect(within(menu).getByText(/cannot see whether a human is driving it/i)).toBeInTheDocument();
		expect(within(menu).queryByText(/free/i)).not.toBeInTheDocument();
	});

	// The one place the lease is enforced in the UI is what is offered: a
	// session that does not hold the device is never given the control that
	// turns driving on, and the effect below switches it off if the lease moves.
	it("names the other holder and offers no way to drive until it is taken over", async () => {
		serveDevices(
			devicesPayload([device({ lease: { state: "held", holder: "other-7" } })], "UDID-A", "the only booted simulator"),
		);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		expect(screen.queryByRole("button", { name: /drive this device/i })).not.toBeInTheDocument();
		const menu = await openMenu();
		expect(within(menu).getByText(/Leased by @other-7/i)).toBeInTheDocument();
	});

	// Taking the device was two presses - the options menu, then the item in it -
	// which is one too many for the thing a person does before they can touch the
	// screen at all. It is a button in the toolbar, so no menu is involved.
	it("claims in a single press, with no menu to open first", async () => {
		serveDevices(devicesPayload([device()], "UDID-A", "the only booted simulator"));
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await userEvent.click(await screen.findByRole("button", { name: /claim to drive/i }));

		expect(screen.queryByRole("menu")).not.toBeInTheDocument();
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/sim-leases",
				expect.objectContaining({ body: { udid: "UDID-A", takeOver: undefined } }),
			),
		);
	});

	/**
	 * 🗝 THE BUG somebody lost real working time to. AO's leases last ten
	 * minutes, so anybody working longer than that loses one and takes it back -
	 * and driving never came back with it. The daemon said the lease was theirs,
	 * the pill said live, and every press vanished in silence, so they had to
	 * ask another person what was wrong.
	 *
	 * Reproduced in the real app before this was written: after a lease lapsed
	 * and was re-acquired, the daemon confirmed the lease was ours again while
	 * the Drive toggle read aria-pressed=false and a press sent 0 requests.
	 */
	it("drives again once a lease that lapsed comes back", async () => {
		serveDevices(devicesPayload([device({ lease: { state: "held", holder: "p-1" } })], "UDID-A", "the only one"));
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();
		const canvas = await screen.findByTestId("sim-canvas");
		stubCanvasBox(canvas);
		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300 });
		fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 100, clientY: 300 });
		await waitFor(() => expect(gestureKinds()).toEqual(["tap"]));

		// The lease lapses. Driving must stop - this half already worked.
		serveDevices(devicesPayload([device({ lease: { state: "unknown", reason: "lapsed" } })], "UDID-A", "the only one"));
		await refresh();
		await waitFor(() => expect(screen.queryByRole("button", { name: /drive this device/i })).not.toBeInTheDocument());

		// And it comes back - re-claimed by this same session.
		serveDevices(devicesPayload([device({ lease: { state: "held", holder: "p-1" } })], "UDID-A", "the only one"));
		await refresh();
		const toggle = await screen.findByRole("button", { name: /drive this device/i });

		// ⚠ The assertion the bug fails: the human turned driving on and never
		// turned it off. The lease is what grants it, and the lease is back.
		await waitFor(() => expect(toggle).toHaveAttribute("aria-pressed", "true"));
		fireEvent.pointerDown(canvas, { pointerId: 2, clientX: 100, clientY: 300 });
		fireEvent.pointerUp(canvas, { pointerId: 2, clientX: 100, clientY: 300 });
		await waitFor(() => expect(gestureKinds()).toEqual(["tap", "tap"]));
	});

	// A press that cannot reach the device must say so. Dropping it in silence
	// is what turned a ten-second fix into asking somebody else for help.
	it("says why a press cannot reach the device instead of dropping it", async () => {
		serveDevices(devicesPayload([device()], "UDID-A", "the only booted simulator"));
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		const canvas = await screen.findByTestId("sim-canvas");
		stubCanvasBox(canvas);

		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300 });
		fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 100, clientY: 300 });

		expect(gestureKinds()).toEqual([]);
		expect(await screen.findByText(/not holding this device/i)).toBeInTheDocument();
	});

	// Claimed, but driving switched off: the other silent case.
	it("says driving is off when that is what is stopping the press", async () => {
		serveDevices(devicesPayload([device({ lease: { state: "held", holder: "p-1" } })], "UDID-A", "the only one"));
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		const canvas = await screen.findByTestId("sim-canvas");
		stubCanvasBox(canvas);

		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300 });
		fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 100, clientY: 300 });

		expect(gestureKinds()).toEqual([]);
		expect(await screen.findByText(/driving is off/i)).toBeInTheDocument();
	});

	// The lease stops two agents driving one device at once; it is not there to
	// lock a person out of their own machine. One press here too - and still
	// named after the holder, so it reads as a decision rather than a slip.
	it("takes the device over in a single press, naming who has it", async () => {
		serveDevices(
			devicesPayload([device({ lease: { state: "held", holder: "other-7" } })], "UDID-A", "the only booted simulator"),
		);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await userEvent.click(await screen.findByRole("button", { name: /take over from @other-7/i }));

		expect(screen.queryByRole("menu")).not.toBeInTheDocument();
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/sim-leases",
				expect.objectContaining({ body: { udid: "UDID-A", takeOver: true } }),
			),
		);
	});

	// An ordinary claim on a device nobody holds must not ask to take anything
	// over: the two refuse for different reasons and mean different things.
	it("never offers to take over a device nobody holds", async () => {
		serveDevices(devicesPayload([device()], "UDID-A", "the only booted simulator"));
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await screen.findByRole("button", { name: /claim to drive/i });
		expect(screen.queryByRole("button", { name: /take over/i })).not.toBeInTheDocument();
	});

	// One control in that slot or none, never two: an offer to claim a device
	// this session already holds is nonsense, and a second button appearing
	// beside the first is the row growing again.
	it("keeps one lease control in the toolbar whatever the lease says", async () => {
		const controls = [/claim to drive/i, /take over from @other-7/i, /drive this device/i];
		for (const [lease, expected] of [
			[undefined, /claim to drive/i],
			[{ state: "held", holder: "other-7" }, /take over from @other-7/i],
			[{ state: "held", holder: "p-1" }, /drive this device/i],
		] as const) {
			serveDevices(devicesPayload([device(lease ? { lease } : {})], "UDID-A", "the only booted simulator"));
			const view = render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
			expect(await screen.findByRole("button", { name: expected })).toBeInTheDocument();
			// Exactly one: two of these at once would be both a wider row and an
			// offer to claim a device this session already holds.
			for (const other of controls.filter((c) => c.source !== expected.source)) {
				expect(screen.queryByRole("button", { name: other })).not.toBeInTheDocument();
			}
			view.unmount();
			sessionStorage.clear();
		}
	});

	// The daemon refuses a takeover while a touch is actually happening, and the
	// human has to be told why rather than left with a button that did nothing.
	it("says why a takeover was refused mid-gesture", async () => {
		serveDevices(
			devicesPayload([device({ lease: { state: "held", holder: "other-7" } })], "UDID-A", "the only booted simulator"),
		);
		postMock.mockImplementation(async (path: string) => {
			if (path.endsWith("/sim-leases")) {
				return { error: { message: "UDID-A has a gesture in flight from @other-7: retry in a moment" } };
			}
			return { error: undefined };
		});
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await userEvent.click(await screen.findByRole("button", { name: /take over from @other-7/i }));

		expect(await screen.findByText(/gesture in flight from @other-7/i)).toBeInTheDocument();
	});

	it("offers driving only once this session holds the lease, and never pre-enabled", async () => {
		serveDevices(
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

	/** The y of every gesture of one kind that reached the daemon, in order. */
	function gesturePoints(kind: string): number[] {
		return postMock.mock.calls
			.filter(([path]) => String(path).endsWith("/gesture"))
			.map(([, options]) => (options as { body?: { kind?: string; y?: number } })?.body)
			.filter((body): body is { kind: string; y: number } => body?.kind === kind)
			.map((body) => body.y);
	}

	beforeEach(() => {
		serveDevices(leased());
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
		expect(screen.getByRole("button", { name: /^home$/i })).toBeEnabled();

		serveDevices(
			devicesPayload([device({ lease: { state: "held", holder: "other-7" } })], "UDID-A", "the only booted simulator"),
		);
		await refresh();

		// The controls stay where they are - a row that grows and shrinks moves
		// the screen under the pointer - but nothing on them can fire.
		await waitFor(() => expect(screen.getByRole("button", { name: /^home$/i })).toBeDisabled());
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
		expect(screen.getByRole("button", { name: /^home$/i })).toBeEnabled();

		serveDevices(
			devicesPayload([device({ lease: { state: "held", holder: "other-7" } })], "UDID-A", "the only booted simulator"),
		);
		await refresh();
		await waitFor(() => expect(screen.getByRole("button", { name: /^home$/i })).toBeDisabled());

		serveDevices(leased());
		await refresh();

		const toggle = await screen.findByRole("button", { name: /drive this device/i });
		expect(toggle).toHaveAttribute("aria-pressed", "false");
		expect(screen.getByRole("button", { name: /^home$/i })).toBeDisabled();
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

	// The bug: a press was refused outright while any other gesture was still in
	// flight, so the drag vanished with nothing to show it had been asked for.
	// The device's own arbitration may still refuse it - and says so when it
	// does - but this side must not swallow it.
	it("starts a drag on a press even while another gesture is still in flight", async () => {
		// A box rather than a `let`: TypeScript narrows a variable only assigned
		// inside a callback to `never`, and the assignment really does happen.
		const pending: { settleHome: (() => void) | null } = { settleHome: null };
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();

		// A home press that has not come back yet.
		postMock.mockImplementation(
			(path: string) =>
				new Promise((resolve) => {
					if (String(path).endsWith("/gesture")) pending.settleHome = () => resolve({ error: undefined });
					else resolve({ error: undefined });
				}),
		);
		await userEvent.click(screen.getByRole("button", { name: /^home$/i }));

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
		fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 100, clientY: 200 });

		await waitFor(() => expect(gestureKinds()).toContain("drag-begin"));
		pending.settleHome?.();
	});

	// The one that made it feel random: clicking into the app from another
	// window loses and regains focus, which rebuilds the frame socket, and every
	// press in the few hundred milliseconds before the first new frame decodes
	// used to be dropped. That is exactly when a human clicks.
	it("drives a press made while the stream is reconnecting after a refocus", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();

		// The window loses focus and gets it straight back, as it does when the
		// human clicks into the app. The socket is torn down and rebuilt.
		vi.spyOn(document, "hasFocus").mockReturnValue(false);
		window.dispatchEvent(new Event("blur"));
		await waitFor(() => expect(openSockets()).toHaveLength(0));
		sessionStorage.clear();
		vi.spyOn(document, "hasFocus").mockReturnValue(true);
		window.dispatchEvent(new Event("focus"));
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		// Deliberately no frame yet: this is the reconnecting window.
		//
		// ⚠ Waited for, not asserted outright. The socket is created
		// synchronously by the mock, so `openSockets()` flips one render before
		// the pill does - and on a slow machine the assertion ran while the pill
		// still showed the state from before the blur. It failed on CI twice
		// while passing every local run, which is the shape of a race rather
		// than a fault.
		await waitFor(() => expect(screen.getByTestId("sim-freshness")).toHaveTextContent(/connecting/i));

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
		fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 100, clientY: 200 });

		await waitFor(() => expect(gestureKinds()).toContain("drag-begin"));
	});

	// A stream that has actually ended is different: the picture will never
	// update again, so a click on it would be a click made blind.
	it("refuses to drive a stream that has ended", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();

		openSockets()[0].onmessage?.({
			data: JSON.stringify({ type: "ended", message: "the device is gone" }),
		} as MessageEvent);
		await screen.findByText(/the device is gone/i);

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
		fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 100, clientY: 200 });
		fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 100, clientY: 200 });

		await new Promise((resolve) => setTimeout(resolve, 30));
		expect(gestureKinds()).toEqual([]);
	});

	// A pointer capture the browser takes back must end the touch, or this side
	// believes a finger is down and ignores every drag after it.
	it("ends the drag when the browser takes the pointer back", async () => {
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
		fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 100, clientY: 200 });
		await waitFor(() => expect(gestureKinds()).toContain("drag-begin"));

		fireEvent.lostPointerCapture(canvas, { pointerId: 1 });
		await waitFor(() => expect(gestureKinds()).toContain("drag-end"));

		// And the next drag still works rather than being swallowed.
		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300 });
		fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 100, clientY: 200 });
		await waitFor(() => expect(gestureKinds().filter((k) => k === "drag-begin")).toHaveLength(2));
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
