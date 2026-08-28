import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SimulatorPanel } from "./SimulatorPanel";

const { getMock, postMock, deleteMock, sessionTask } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	deleteMock: vi.fn(),
	// The task this pane belongs to, which is how a lease held by the crewmate
	// gets named by its ROLE instead of by a raw session id. Undefined - the
	// default - is a session with no crew, i.e. every solo task.
	sessionTask: { value: undefined as unknown },
}));

vi.mock("../hooks/useSessionTask", () => ({ useSessionTask: () => sessionTask.value }));

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

// jsdom has no window server, so visibility is a property to define rather than
// a state to reach. ⚠ This is why every claim about what actually streams was
// also checked in the real app: jsdom cannot tell a covered window from a
// focused one.
function setVisibility(state: "visible" | "hidden") {
	Object.defineProperty(document, "visibilityState", { configurable: true, value: state });
	document.dispatchEvent(new Event("visibilitychange"));
}

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
	setVisibility("visible");
	// jsdom has no 2d context; the panel only needs the call not to throw.
	HTMLCanvasElement.prototype.getContext = vi.fn().mockReturnValue({ drawImage: vi.fn() });
});

afterEach(() => {
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

describe("SimulatorPanel device selection", () => {
	// This used to be a dead end - the panel said AO never boots one and left
	// the human to go and do it in Simulator.app. Booting is now something this
	// tab does, so the empty state points at the control that does it.
	it("says nothing is booted, and sends you to the picker to boot one", async () => {
		serveDevices(devicesPayload([device({ state: "Shutdown" })], null, "no simulator is booted"));
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		expect(await screen.findByText(/no simulator is booted/i)).toBeInTheDocument();
		expect(screen.getByText(/pick one above to boot it/i)).toBeInTheDocument();
		expect(screen.queryByText(/AO never boots/i)).not.toBeInTheDocument();
		// Nothing is watched, so nothing is captured: offering to boot a device
		// is not the same as opening a socket to one.
		expect(openSockets()).toHaveLength(0);
	});

	// "No simulator is booted" is a claim about the machine. While nobody is
	// looking nothing has been asked, so making that claim would state something
	// AO never checked.
	it("does not claim there is no simulator when it has not looked", async () => {
		serveDevices(devicesPayload([], null, "no simulator is booted"));
		render(<SimulatorPanel isActive={false} sessionId="p-1" />, { wrapper });

		expect(
			await screen.findByText(/The Device tab is not the one on screen, so nothing is being captured/i),
		).toBeInTheDocument();
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
		await userEvent.click(screen.getByRole("button", { name: /simulator to watch/i }));
		await userEvent.click(await screen.findByRole("button", { name: /watch iPhone 17 Pro$/i }));
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

	// The whole point of the tab: a live view is watched while you do something
	// else. Blur used to close the socket, which made it a screenshot that
	// refreshed when observed.
	it("keeps capturing while the window is not focused", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();

		// What a blurred window really looks like: `hasFocus` goes false and the
		// blur event fires. Stubbed even though the hook no longer reads it, so
		// that putting focus back into the rule fails here rather than shipping.
		vi.spyOn(document, "hasFocus").mockReturnValue(false);
		window.dispatchEvent(new Event("blur"));
		await new Promise((resolve) => setTimeout(resolve, 20));

		expect(openSockets()).toHaveLength(1);
		expect(sockets).toHaveLength(1);
		expect(screen.getByTestId("sim-freshness")).toHaveTextContent(/live/i);
	});

	// Regaining focus must not rebuild a healthy socket either: a rebuild is a
	// new capture process, a fresh keyframe wait and a few hundred milliseconds
	// of a picture that cannot be clicked.
	it("does not rebuild a healthy stream when the window is clicked back into", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();

		window.dispatchEvent(new Event("blur"));
		window.dispatchEvent(new Event("focus"));
		await new Promise((resolve) => setTimeout(resolve, 20));

		expect(sockets).toHaveLength(1);
		expect(openSockets()).toHaveLength(1);
	});

	// Hidden is not the same as unfocused: minimised, hidden with Cmd+H, on
	// another Space or covered outright means nobody can see the screen, and
	// "no viewer, no polling" is the part of the old rule that is kept.
	it("stops capturing while the window is hidden", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));

		setVisibility("hidden");
		await waitFor(() => expect(openSockets()).toHaveLength(0));
	});

	// ⚠ H.264 has no independent frames, so a resumed stream must be fed a
	// complete start - the avcC description, then a keyframe - before anything
	// is decoded. A delta decoded against a decoder nobody configured is a
	// corrupted picture, not a late one.
	it("resumes with a complete start when the window comes back", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();

		setVisibility("hidden");
		await waitFor(() => expect(openSockets()).toHaveLength(0));
		decoderCalls.length = 0;
		decodedKinds.length = 0;

		setVisibility("visible");
		await waitFor(() => expect(openSockets()).toHaveLength(1));

		const socket = openSockets()[0];
		// A delta ahead of the description is dropped rather than decoded.
		socket.onmessage?.({ data: message(KIND_DELTA, [0x99]) } as MessageEvent);
		expect(decodedKinds).toHaveLength(0);

		socket.onmessage?.({ data: message(KIND_DESCRIPTION, AVCC) } as MessageEvent);
		expect(decoderCalls).toHaveLength(1);
		socket.onmessage?.({ data: message(KIND_KEYFRAME, [0x00]) } as MessageEvent);
		expect(decodedKinds).toEqual(["key"]);
		await waitFor(() => expect(screen.getByTestId("sim-freshness")).toHaveTextContent(/live/i));
	});

	// A stream that died while the human was away has to come back on its own
	// when they return. Blur no longer rebuilds the socket, so nothing else
	// would - and the price of getting this wrong is a dead picture that looks
	// exactly like a live one until you touch it.
	it("retries a stream that ended once the human comes back to the window", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();

		openSockets()[0].onmessage?.({
			data: JSON.stringify({ type: "ended", message: "the capture process failed" }),
		} as MessageEvent);
		await waitFor(() => expect(screen.getByTestId("sim-freshness")).toHaveTextContent(/ended/i));

		window.dispatchEvent(new Event("focus"));
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		expect(sockets).toHaveLength(2);
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

/**
 * What the pill says has to match why the stream is stopped.
 *
 * "Stopped because you cannot see it" and "stopped because it broke" are the
 * same word - `paused` - to anyone reading a status line, and a human who
 * cannot tell them apart reports the second as a bug.
 */
describe("SimulatorPanel says which kind of stopped it is", () => {
	beforeEach(() => {
		serveDevices(devicesPayload([device()], "UDID-A", "the only booted simulator"));
	});

	it("says the tab is off screen, not that the stream is paused", async () => {
		render(<SimulatorPanel isActive={false} sessionId="p-1" />, { wrapper });

		await waitFor(() => expect(screen.getByTestId("sim-freshness")).toHaveTextContent(/off screen/i));
		expect(screen.getByTestId("sim-freshness")).not.toHaveTextContent(/paused/i);
	});

	it("says the window is hidden, and that it resumes on its own", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();

		setVisibility("hidden");

		await waitFor(() => expect(screen.getByTestId("sim-freshness")).toHaveTextContent(/hidden/i));
		expect(screen.getByText(/resumes as soon as the window is back on screen/i)).toBeInTheDocument();
	});

	it("says ended with the reason when the stream breaks, and never blames visibility", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();

		openSockets()[0].onmessage?.({
			data: JSON.stringify({ type: "ended", message: "the device is gone" }),
		} as MessageEvent);

		await waitFor(() => expect(screen.getByTestId("sim-freshness")).toHaveTextContent(/ended/i));
		expect(await screen.findByText(/the device is gone/i)).toBeInTheDocument();
		expect(screen.queryByText(/nothing is being captured/i)).not.toBeInTheDocument();
	});

	it("says idle while no device has been chosen", async () => {
		serveDevices(devicesPayload([device(), device({ udid: "UDID-B", name: "iPhone 17 Pro" })], null, "2 are booted"));
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await waitFor(() => expect(screen.getByTestId("sim-freshness")).toHaveTextContent(/idle/i));
		expect(openSockets()).toHaveLength(0);
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

	// The one that made it feel random: coming back to a window that was hidden
	// rebuilds the frame socket, and every press in the few hundred milliseconds
	// before the first new frame decodes used to be dropped. That is exactly
	// when a human clicks.
	it("drives a press made while the stream is reconnecting after the window comes back", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();

		// The window is hidden and shown again, as it is when the human comes back
		// from another Space. The socket is torn down and rebuilt.
		setVisibility("hidden");
		await waitFor(() => expect(openSockets()).toHaveLength(0));
		sessionStorage.clear();
		setVisibility("visible");
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

describe("SimulatorPanel — who holds the device, in the role's words", () => {
	// Two agents on one task can hold two simulators at once, and "who has which"
	// is asked here more than anywhere else. `@agent-orchestrator-241` is the
	// wrong vocabulary for the answer when the holder is the crewmate sitting in
	// the switcher one strip above.
	const crewDev = { id: "p-1", crew: { id: "p-1", role: "dev" } };
	const crewQa = { id: "qa-9", crew: { id: "p-1", role: "qa" } };

	beforeEach(() => {
		sessionTask.value = { dev: crewDev, qa: crewQa, members: [crewDev, crewQa], isCrew: true };
	});

	afterEach(() => {
		sessionTask.value = undefined;
	});

	it("names the crewmate by role, and keeps the raw id in the tooltip", async () => {
		serveDevices(
			devicesPayload([device({ lease: { state: "held", holder: "qa-9" } })], "UDID-A", "the only booted simulator"),
		);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		const menu = await openMenu();
		const line = within(menu).getByText(/Leased by qa\./i);
		expect(line).toBeInTheDocument();
		// The id is never lost - it rides along on the tooltip.
		expect(line).toHaveAttribute("title", "@qa-9");
		expect(within(menu).queryByText(/@qa-9\./i)).not.toBeInTheDocument();
	});

	it("keeps the `@id` for a holder that is NOT on this task — a bare role would name the wrong task", async () => {
		serveDevices(
			devicesPayload([device({ lease: { state: "held", holder: "other-7" } })], "UDID-A", "the only booted simulator"),
		);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		const menu = await openMenu();
		expect(within(menu).getByText(/Leased by @other-7/i)).toBeInTheDocument();
	});
});

/**
 * Pinch by hand: hold Option and the pane puts two fingers on the screen, one
 * under the pointer and one mirrored through the middle, exactly as
 * Simulator.app does (measured - see ../lib/pinch.ts).
 *
 * The box these tests give the canvas is 200x400 over a 1320x2868 frame, so the
 * picture is 184.1 wide with a 7.95px bar each side: clientX 100 is the middle
 * of the screen and clientY 300 is three quarters of the way down.
 */
describe("SimulatorPanel pinch", () => {
	// ⚠ The dots are drawn over the PICTURE, and where the picture is comes from
	// the stage's measured size - the same `fitDevice` call the canvas is sized
	// from, so the two cannot disagree about where the screen is. The repo's
	// shared ResizeObserver stub never reports, so the pane here believes it has
	// not been measured yet and draws no overlay at all. One that reports the box
	// `stubCanvasBox` already gives the canvas puts these tests on the path a
	// browser takes.
	const realResizeObserver = window.ResizeObserver;
	beforeEach(() => {
		window.ResizeObserver = class {
			constructor(private readonly cb: ResizeObserverCallback) {}
			observe() {
				this.cb([{ contentRect: { width: 200, height: 400 } } as ResizeObserverEntry], this);
			}
			unobserve() {}
			disconnect() {}
		} as unknown as typeof ResizeObserver;
	});
	afterEach(() => {
		window.ResizeObserver = realResizeObserver;
	});

	beforeEach(() => {
		serveDevices(
			devicesPayload([device({ lease: { state: "held", holder: "p-1" } })], "UDID-A", "the only booted simulator"),
		);
	});

	/** A gesture body's kind and both of its contacts, to a floating point's tolerance. */
	function expectGrip(body: Record<string, unknown>, kind: string, a: [number, number], b: [number, number]) {
		expect(body.kind).toBe(kind);
		expect(Number(body.x)).toBeCloseTo(a[0], 6);
		expect(Number(body.y)).toBeCloseTo(a[1], 6);
		expect(Number(body.x2)).toBeCloseTo(b[0], 6);
		expect(Number(body.y2)).toBeCloseTo(b[1], 6);
	}

	/** Every gesture body that reached the daemon, in order. */
	function gestureBodies(): Record<string, unknown>[] {
		return postMock.mock.calls
			.filter(([path]) => String(path).endsWith("/gesture"))
			.map(([, options]) => (options as { body?: Record<string, unknown> })?.body ?? {});
	}

	async function drivableCanvas() {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();
		const canvas = await screen.findByTestId("sim-canvas");
		stubCanvasBox(canvas);
		return canvas;
	}

	it("puts two fingers down, mirrored through the middle of the screen", async () => {
		const canvas = await drivableCanvas();

		// ⚠ Both contacts land on the PRESS. A pinch has no tap to be mistaken
		// for, so unlike a one-finger drag it does not wait to see movement -
		// and Simulator.app's very first frame reports two touches too.
		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300, altKey: true });
		await waitFor(() => expect(gestureKinds()).toContain("pinch-begin"));
		expectGrip(gestureBodies()[0], "pinch-begin", [0.5, 0.75], [0.5, 0.25]);

		fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 100, clientY: 360, altKey: true });
		await waitFor(() => expect(gestureKinds()).toContain("pinch-move"));
		// Spreading: the pointer's finger went down, so the other went up by the
		// same amount and the gap grew.
		expectGrip(gestureBodies()[1], "pinch-move", [0.5, 0.9], [0.5, 0.1]);

		fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 100, clientY: 360, altKey: true });
		await waitFor(() => expect(gestureKinds()).toContain("pinch-end"));
		expectGrip(gestureBodies()[2], "pinch-end", [0.5, 0.9], [0.5, 0.1]);
	});

	// ⚠ The regression this pane could most easily have shipped. A pinch shares
	// the whole held-touch path with an ordinary drag, so a mistake there is a
	// mistake in the gesture people use all day.
	it("still sends an ordinary one-finger drag when Option is not held", async () => {
		const canvas = await drivableCanvas();

		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300 });
		expect(gestureKinds()).toEqual([]);
		fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 100, clientY: 200 });
		await waitFor(() => expect(gestureKinds()).toContain("drag-begin"));
		fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 100, clientY: 200 });
		await waitFor(() => expect(gestureKinds()).toContain("drag-end"));

		for (const body of gestureBodies()) {
			expect(body).not.toHaveProperty("x2");
			expect(String(body.kind)).toMatch(/^drag-/);
		}
	});

	// And a tap is still a tap: a press that never moves, with no Option, must
	// not have become a gesture with two fingers in it.
	it("still sends a tap for a press that never moves", async () => {
		const canvas = await drivableCanvas();

		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300 });
		fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 100, clientY: 300 });
		await waitFor(() => expect(gestureKinds()).toEqual(["tap"]));
	});

	// 🗝 The divergence from Simulator.app, and the reason for it. Releasing
	// Option mid-drag there leaves BOTH CONTACTS DOWN - measured: no further
	// move, no end, the device stops responding until something else touches it.
	// Here the gesture you started is the gesture you finish.
	it("finishes as a pinch when Option is let go mid-gesture", async () => {
		const canvas = await drivableCanvas();

		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300, altKey: true });
		await waitFor(() => expect(gestureKinds()).toContain("pinch-begin"));
		// Option is released; the button is still down.
		fireEvent.keyUp(window, { key: "Alt", altKey: false });
		fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 100, clientY: 360, altKey: false });
		fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 100, clientY: 360, altKey: false });

		await waitFor(() => expect(gestureKinds()).toEqual(["pinch-begin", "pinch-move", "pinch-end"]));
	});

	// The other half of the same rule: a touch that went down as one finger stays
	// one finger. The daemon refuses a held touch that changes its count, and
	// there is no honest way to add a contact that never landed.
	it("does not turn a drag already under way into a pinch", async () => {
		const canvas = await drivableCanvas();

		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300 });
		fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 100, clientY: 200 });
		await waitFor(() => expect(gestureKinds()).toContain("drag-begin"));
		fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 100, clientY: 100, altKey: true });
		fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 100, clientY: 100, altKey: true });

		await waitFor(() => expect(gestureKinds()).toContain("drag-end"));
		expect(gestureKinds().every((kind) => kind.startsWith("drag-"))).toBe(true);
	});

	// Shift moves the pair instead of spreading it, which is the only way to zoom
	// about anywhere but the middle. Simulator.app does the same.
	it("moves both fingers together while Shift is held", async () => {
		const canvas = await drivableCanvas();

		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300, altKey: true });
		await waitFor(() => expect(gestureKinds()).toContain("pinch-begin"));
		fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 140, clientY: 300, altKey: true, shiftKey: true });
		await waitFor(() => expect(gestureKinds()).toContain("pinch-move"));

		const before = gestureBodies()[0];
		const after = gestureBodies()[1];
		// The gap is unchanged - both fingers travelled the same way.
		const gap = (b: Record<string, unknown>) => Number(b.y2) - Number(b.y);
		expect(gap(after)).toBeCloseTo(gap(before), 6);
		expect(Number(after.x) - Number(before.x)).toBeCloseTo(Number(after.x2) - Number(before.x2), 6);
		expect(Number(after.x)).toBeGreaterThan(Number(before.x));
	});

	// A press that never moves is a tap for one finger - and must never be one
	// for two. There is no two-finger tap here to send, and the daemon refuses
	// contacts that land on the same spot, so a pinch that went nowhere has to
	// end as a pinch rather than fall through to the tap.
	it("does not turn an Option press that never moved into a tap", async () => {
		const canvas = await drivableCanvas();

		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300, altKey: true });
		fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 100, clientY: 300, altKey: true });
		await waitFor(() => expect(gestureKinds()).toContain("pinch-end"));
		expect(gestureKinds()).not.toContain("tap");
	});

	// 🗝 Every way a human abandons a pinch has to lift BOTH contacts, because a
	// contact left down wedges the device's input until it is rebooted. These are
	// the ones this side can see coming; the daemon's watchdog is the backstop
	// for the ones it cannot.
	it.each([
		["the pointer capture is taken back", (canvas: HTMLElement) => fireEvent.lostPointerCapture(canvas)],
		["the pointer is cancelled", (canvas: HTMLElement) => fireEvent.pointerCancel(canvas, { pointerId: 1 })],
		["the window loses focus", () => fireEvent.blur(window)],
	])("releases both fingers when %s", async (_why, abandon) => {
		const canvas = await drivableCanvas();

		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300, altKey: true });
		await waitFor(() => expect(gestureKinds()).toContain("pinch-begin"));
		fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 100, clientY: 360, altKey: true });
		await waitFor(() => expect(gestureKinds()).toContain("pinch-move"));

		abandon(canvas);

		await waitFor(() => expect(gestureKinds()).toContain("pinch-end"));
		// ⚠ Released where the fingers actually were, as a PAIR. An end that
		// named one finger would be a grip change: the daemon refuses it and has
		// to lift what is down itself, which is the recovery path rather than
		// the ordinary one.
		expectGrip(gestureBodies().find((b) => b.kind === "pinch-end") ?? {}, "pinch-end", [0.5, 0.9], [0.5, 0.1]);
	});

	// Driving being taken away mid-pinch - the tab switched, the lease lost - has
	// to lift the fingers too. It used to be given up locally, leaving the
	// release to the daemon's two-second watchdog: two seconds of a device that
	// answers nothing, which with two fingers down is felt as a simulator that
	// has stopped working.
	it("releases both fingers when the tab stops being the one on screen", async () => {
		const view = render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();
		const canvas = await screen.findByTestId("sim-canvas");
		stubCanvasBox(canvas);

		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300, altKey: true });
		await waitFor(() => expect(gestureKinds()).toContain("pinch-begin"));

		// ⚠ The pane stays MOUNTED when its tab goes off - on purpose, so the
		// chosen device survives the trip - so nothing else would ever notice.
		// No pointerup can arrive at a pane nobody can point at.
		view.rerender(<SimulatorPanel isActive={false} sessionId="p-1" />);

		await waitFor(() => expect(gestureKinds()).toContain("pinch-end"));
	});

	// And a session switch keys this pane away entirely, which is the one path
	// with nothing left afterwards to notice a finger is still down.
	it("releases both fingers when the pane is unmounted", async () => {
		const view = render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();
		const canvas = await screen.findByTestId("sim-canvas");
		stubCanvasBox(canvas);

		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 300, altKey: true });
		await waitFor(() => expect(gestureKinds()).toContain("pinch-begin"));

		view.unmount();

		await waitFor(() => expect(gestureKinds()).toContain("pinch-end"));
	});

	// The dots are the whole of what tells somebody the tool is armed and where
	// the fingers will land, so they appear on the key and go away with it.
	it("shows the two dots while Option is held over the screen", async () => {
		const canvas = await drivableCanvas();
		expect(screen.queryByTestId("sim-pinch-dots")).not.toBeInTheDocument();

		fireEvent.pointerEnter(canvas);
		fireEvent.keyDown(window, { key: "Alt", altKey: true });
		expect(await screen.findByTestId("sim-pinch-dots")).toBeInTheDocument();
		expect(screen.getByTestId("sim-pinch-dot-a")).toBeInTheDocument();
		expect(screen.getByTestId("sim-pinch-dot-b")).toBeInTheDocument();

		fireEvent.keyUp(window, { key: "Alt", altKey: false });
		await waitFor(() => expect(screen.queryByTestId("sim-pinch-dots")).not.toBeInTheDocument());
	});

	// ⚠ A window that loses focus never delivers the keyup. Without this the
	// tool would stay armed after a Cmd-Tab and the next ordinary click would put
	// two fingers on the device.
	it("disarms when the window loses focus without a keyup", async () => {
		const canvas = await drivableCanvas();
		fireEvent.pointerEnter(canvas);
		fireEvent.keyDown(window, { key: "Alt", altKey: true });
		expect(await screen.findByTestId("sim-pinch-dots")).toBeInTheDocument();

		fireEvent.blur(window);
		await waitFor(() => expect(screen.queryByTestId("sim-pinch-dots")).not.toBeInTheDocument());
	});
});

// 🗝 The anchor starts at the middle of the screen, so a press ON the middle puts
// both fingers on the same spot. The daemon refuses that - a pinch that lands as
// one touch sends events and changes nothing - and being told off for starting a
// zoom where a zoom naturally starts is a poor answer. So the contacts are drawn
// and the touch waits until there are two of them to put down.
//
// ⚠ Found on a real device, not in a test: the first Option-drag driven through
// the packaged app started at 0.5,0.5 and was refused before anything landed.
describe("SimulatorPanel pinch from the exact middle", () => {
	beforeEach(() => {
		window.ResizeObserver = class {
			constructor(private readonly cb: ResizeObserverCallback) {}
			observe() {
				this.cb([{ contentRect: { width: 200, height: 400 } } as ResizeObserverEntry], this);
			}
			unobserve() {}
			disconnect() {}
		} as unknown as typeof ResizeObserver;
		serveDevices(
			devicesPayload([device({ lease: { state: "held", holder: "p-1" } })], "UDID-A", "the only booted simulator"),
		);
	});

	it("waits for the fingers to be two before putting them down", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();
		const canvas = await screen.findByTestId("sim-canvas");
		stubCanvasBox(canvas);

		// clientY 200 of a 400-tall box is the exact middle of the screen, where
		// the two contacts coincide.
		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 200, altKey: true });
		// Nothing is sent, and nothing is refused: there is no gesture yet.
		expect(
			postMock.mock.calls.filter(([path]) => String(path).endsWith("/gesture")),
			"a pinch with both fingers on one spot was sent to the device",
		).toHaveLength(0);
		// The dots are drawn all the same, so the human can see where the fingers
		// are and that they are on top of each other.
		expect(await screen.findByTestId("sim-pinch-dots")).toBeInTheDocument();

		// Moving off the middle separates them, and that is when the touch lands.
		fireEvent.pointerMove(canvas, { pointerId: 1, clientX: 100, clientY: 300, altKey: true });
		await waitFor(() => expect(gestureKinds()).toEqual(["pinch-begin"]));
		fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 100, clientY: 300, altKey: true });
		await waitFor(() => expect(gestureKinds()).toEqual(["pinch-begin", "pinch-end"]));
	});

	// And a press that never leaves the middle sends nothing at all - not a
	// pinch, and not a tap either.
	it("sends nothing for a press that never leaves the middle", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();
		const canvas = await screen.findByTestId("sim-canvas");
		stubCanvasBox(canvas);

		fireEvent.pointerDown(canvas, { pointerId: 1, clientX: 100, clientY: 200, altKey: true });
		fireEvent.pointerUp(canvas, { pointerId: 1, clientX: 100, clientY: 200, altKey: true });
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(gestureKinds()).toEqual([]);
	});
});
