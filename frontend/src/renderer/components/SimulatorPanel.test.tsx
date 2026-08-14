import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
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

beforeEach(() => {
	sockets.length = 0;
	getMock.mockReset();
	postMock.mockReset().mockResolvedValue({ error: undefined });
	deleteMock.mockReset().mockResolvedValue({ error: undefined });
	vi.stubGlobal("WebSocket", MockWebSocket);
	vi.spyOn(document, "hasFocus").mockReturnValue(true);
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

	// With two booted there is nothing preselected, and the label has to say what
	// to do rather than "not watching", which reads like a failure.
	it("asks for a choice rather than reporting that it is not watching", async () => {
		getMock.mockResolvedValue(
			devicesPayload(
				[device(), device({ udid: "UDID-B", name: "iPhone 17 Pro" })],
				null,
				"2 simulators are booted, so there is no unambiguous default",
			),
		);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await waitFor(() =>
			expect(screen.getByTestId("sim-freshness")).toHaveTextContent(/choose a simulator above to start watching/i),
		);
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

describe("SimulatorPanel lease truth", () => {
	it("says unknown with the reason, never free, and offers to claim", async () => {
		getMock.mockResolvedValue(devicesPayload([device()], "UDID-A", "the only booted simulator"));
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		expect(await screen.findByText(/Lease: unknown/i)).toBeInTheDocument();
		expect(screen.getByText(/cannot see whether a human is driving it/i)).toBeInTheDocument();
		expect(screen.queryByText(/free/i)).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: /claim to drive/i })).toBeInTheDocument();
	});

	it("names the other holder and offers no way to drive", async () => {
		getMock.mockResolvedValue(
			devicesPayload([device({ lease: { state: "held", holder: "other-7" } })], "UDID-A", "the only booted simulator"),
		);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		expect(await screen.findByText(/Leased by @other-7/i)).toBeInTheDocument();
		expect(screen.queryByRole("checkbox", { name: /drive this device/i })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /claim to drive/i })).not.toBeInTheDocument();
	});

	it("offers driving only once this session holds the lease, and never pre-enabled", async () => {
		getMock.mockResolvedValue(
			devicesPayload([device({ lease: { state: "held", holder: "p-1" } })], "UDID-A", "the only booted simulator"),
		);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		const toggle = await screen.findByRole("checkbox", { name: /drive this device/i });
		expect(toggle).not.toBeChecked();
	});
});

describe("SimulatorPanel driving", () => {
	const leased = () =>
		devicesPayload([device({ lease: { state: "held", holder: "p-1" } })], "UDID-A", "the only booted simulator");

	async function turnDrivingOn() {
		const toggle = await screen.findByRole("checkbox", { name: /drive this device/i });
		await userEvent.click(toggle);
		return toggle;
	}

	function makeLive() {
		// A tap is only accepted on a live screen, so give the stream one frame.
		const socket = openSockets()[0];
		socket.onmessage?.({ data: new ArrayBuffer(8) } as MessageEvent);
	}

	beforeEach(() => {
		getMock.mockResolvedValue(leased());
		vi.stubGlobal("createImageBitmap", vi.fn().mockResolvedValue({ width: 100, height: 200, close: () => {} }));
		// jsdom has no 2d context; the panel only needs the call not to throw.
		HTMLCanvasElement.prototype.getContext = vi.fn().mockReturnValue({ drawImage: vi.fn() });
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

		await userEvent.click(screen.getByRole("button", { name: /home/i }));
		// Matched exactly: the panel's own guidance also mentions mid-gesture, and
		// a loose matcher here would pass on the help text alone.
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
		expect(screen.getByRole("button", { name: /home/i })).toBeInTheDocument();

		getMock.mockResolvedValue(
			devicesPayload([device({ lease: { state: "held", holder: "other-7" } })], "UDID-A", "the only booted simulator"),
		);
		await userEvent.click(screen.getByRole("button", { name: /refresh simulators/i }));

		await waitFor(() => expect(screen.queryByRole("button", { name: /home/i })).not.toBeInTheDocument());
		expect(screen.queryByRole("checkbox", { name: /drive this device/i })).not.toBeInTheDocument();
	});

	// The subtle one: a lease that goes away and comes back must not bring
	// driving back with it. Otherwise the toggle silently re-enables itself for a
	// device the human has not looked at since, and the next click lands blind.
	it("makes the human opt in again after the lease is lost and regained", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();
		expect(screen.getByRole("button", { name: /home/i })).toBeInTheDocument();

		getMock.mockResolvedValue(
			devicesPayload([device({ lease: { state: "held", holder: "other-7" } })], "UDID-A", "the only booted simulator"),
		);
		await userEvent.click(screen.getByRole("button", { name: /refresh simulators/i }));
		await waitFor(() => expect(screen.queryByRole("button", { name: /home/i })).not.toBeInTheDocument());

		getMock.mockResolvedValue(leased());
		await userEvent.click(screen.getByRole("button", { name: /refresh simulators/i }));

		const toggle = await screen.findByRole("checkbox", { name: /drive this device/i });
		expect(toggle).not.toBeChecked();
		expect(screen.queryByRole("button", { name: /home/i })).not.toBeInTheDocument();
	});

	it("sends a home press through the arbitrated gesture route", async () => {
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });
		await waitFor(() => expect(openSockets()).toHaveLength(1));
		makeLive();
		await turnDrivingOn();

		await userEvent.click(screen.getByRole("button", { name: /home/i }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/sim-devices/{udid}/gesture",
				expect.objectContaining({ body: { kind: "button", name: "home" } }),
			),
		);
	});
});
