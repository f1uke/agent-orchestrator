import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
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

const UDID = "UDID-A";

const bootedDevice = (lease: Record<string, unknown>) => ({
	udid: UDID,
	name: "iPhone 17 Pro Max",
	runtime: "iOS 26.3",
	runtimeIdentifier: "com.apple.CoreSimulator.SimRuntime.iOS-26-3",
	state: "Booted",
	available: true,
	default: true,
	lease,
});

const heldByUs = { state: "held", holder: "p-1" };
const heldByNobody = { state: "unknown", reason: "no AO session holds this device" };

/** A recording as the daemon reports it. */
function recordingBody(over: Partial<{ stoppedAt: string; sessionId: string; stepCount: number; name: string }> = {}) {
	return {
		data: {
			recording: {
				udid: UDID,
				sessionId: over.sessionId ?? "p-1",
				name: over.name ?? "",
				startedAt: "2026-08-18T04:57:00Z",
				stoppedAt: over.stoppedAt,
				updatedAt: "2026-08-18T04:57:30Z",
			},
			stepCount: over.stepCount ?? 0,
			steps: [],
		},
		error: undefined,
		response: { status: 200 },
	};
}

const noRecording = { data: undefined, error: { message: "not found" }, response: { status: 404 } };

function flow(over: Partial<Record<string, unknown>> = {}) {
	return {
		name: "login-to-portfolio",
		fileName: "login-to-portfolio-20260818-045722.711Z.yaml",
		path: "/data/sim/p-1/flows/login-to-portfolio-20260818-045722.711Z.yaml",
		recordedAt: "2026-08-18T04:57:22.711Z",
		timeFromFileName: true,
		steps: 14,
		review: 2,
		countsKnown: true,
		bytes: 812,
		...over,
	};
}

/**
 * The daemon, as far as this panel is concerned. Routing by path rather than
 * one blanket answer, because the recording counter and the flows list are
 * different questions asked of the same client.
 */
type World = {
	lease: Record<string, unknown>;
	recording: unknown;
	flows: unknown[];
};

function serve(world: World) {
	getMock.mockImplementation(async (path: string) => {
		if (path === "/api/v1/sim/devices") {
			return {
				data: { devices: [bootedDevice(world.lease)], defaultUdid: UDID, defaultReason: "the only booted simulator" },
				error: undefined,
				response: { status: 200 },
			};
		}
		if (path.includes("sim-recordings")) return world.recording;
		if (path.includes("sim-flows")) {
			return { data: { flows: world.flows }, error: undefined, response: { status: 200 } };
		}
		throw new Error(`unexpected GET ${path}`);
	});
}

function wrapper({ children }: { children: ReactNode }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const recordButton = () => screen.getByTestId("sim-record-toggle");
const stepCount = () => screen.getByTestId("sim-record-count").textContent;

async function openRecordings() {
	await userEvent.click(screen.getByTestId("sim-recordings-trigger"));
	return screen.findByTestId("sim-recordings-popover");
}

beforeEach(() => {
	getMock.mockReset();
	postMock.mockReset().mockResolvedValue({ error: undefined });
	deleteMock.mockReset().mockResolvedValue({ error: undefined });
	patchMock.mockReset().mockResolvedValue({ error: undefined });
	vi.stubGlobal("WebSocket", MockWebSocket);
	sessionStorage.clear();
	HTMLCanvasElement.prototype.getContext = vi.fn().mockReturnValue({ drawImage: vi.fn() });
});

afterEach(() => {
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

describe("starting and stopping", () => {
	it("starts a recording through the same route `ao sim record start` uses", async () => {
		serve({ lease: heldByUs, recording: noRecording, flows: [] });
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await waitFor(() => expect(recordButton()).toBeEnabled());
		await userEvent.click(recordButton());

		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/sim-recordings/{udid}", {
			params: { path: { sessionId: "p-1", udid: UDID } },
			body: {},
		});
	});

	it("stops through the same route, and says where the flow went", async () => {
		serve({ lease: heldByUs, recording: recordingBody({ stepCount: 14 }), flows: [] });
		deleteMock.mockResolvedValue({
			data: { recording: {}, stepCount: 14, steps: [], flow: flow() },
			error: undefined,
		});
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await waitFor(() => expect(recordButton()).toHaveAttribute("aria-pressed", "true"));
		await userEvent.click(recordButton());

		expect(deleteMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/sim-recordings/{udid}", {
			params: { path: { sessionId: "p-1", udid: UDID }, query: {} },
		});

		// What stop reports: where it is, how big, and how much of it a human
		// has to check before trusting it.
		const summary = await screen.findByTestId("sim-stop-summary");
		expect(summary).toHaveTextContent("14 steps");
		expect(summary).toHaveTextContent('2 marked "# REVIEW:"');
		expect(summary).toHaveTextContent("login-to-portfolio-20260818-045722.711Z.yaml");
		// The path is copyable rather than merely displayed - it is what gets
		// pasted into a message to a worker.
		expect(within(summary).getByRole("button", { name: /Copy recording path/ })).toBeInTheDocument();
	});

	it("does not claim a review count a clean flow does not have", async () => {
		serve({ lease: heldByUs, recording: recordingBody({ stepCount: 3 }), flows: [] });
		deleteMock.mockResolvedValue({
			data: { flow: flow({ review: 0, steps: 3 }) },
			error: undefined,
		});
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await waitFor(() => expect(recordButton()).toHaveAttribute("aria-pressed", "true"));
		await userEvent.click(recordButton());

		const summary = await screen.findByTestId("sim-stop-summary");
		expect(summary).toHaveTextContent("nothing marked for review");
	});
});

describe("the live step count", () => {
	// 🗝 The failure this whole control exists to expose: a recorder that is not
	// wired to the daemon, where start succeeds and the count never moves. The
	// number has to come from the daemon, so it can fail to move.
	it("shows the count the daemon reports, not one it counted itself", async () => {
		const world: World = { lease: heldByUs, recording: recordingBody({ stepCount: 0 }), flows: [] };
		serve(world);
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await waitFor(() => expect(stepCount()).toBe("0"));

		world.recording = recordingBody({ stepCount: 7 });
		await waitFor(() => expect(stepCount()).toBe("7"), { timeout: 4000 });

		world.recording = recordingBody({ stepCount: 12 });
		await waitFor(() => expect(stepCount()).toBe("12"), { timeout: 4000 });
	});

	it("carries the count in the accessible name, so it is not colour or shape alone", async () => {
		serve({ lease: heldByUs, recording: recordingBody({ stepCount: 1 }), flows: [] });
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await waitFor(() => expect(recordButton()).toHaveAccessibleName("Stop recording - 1 step captured"));
	});

	it("keeps a fixed-width slot so a growing count cannot nudge its neighbours", async () => {
		serve({ lease: heldByUs, recording: recordingBody({ stepCount: 9 }), flows: [] });
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await waitFor(() => expect(stepCount()).toBe("9"));
		const slot = screen.getByTestId("sim-record-count");
		// jsdom cannot measure this; what it CAN pin is that the width is fixed
		// and the figures are tabular, which is the mechanism. The measurement
		// itself is in the real-browser check.
		expect(slot.className).toContain("w-[3.25ch]");
		expect(slot.className).toContain("tabular-nums");
	});

	// Above two digits the count is abbreviated rather than allowed to widen:
	// this row wraps at the rail's narrowest, and one extra character can cost
	// the device screen a line of height.
	it("abbreviates rather than widening past 99", async () => {
		serve({ lease: heldByUs, recording: recordingBody({ stepCount: 140 }), flows: [] });
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await waitFor(() => expect(stepCount()).toBe("99+"));
	});
});

describe("who owns the recording", () => {
	// One recording per session, not one per surface: what a terminal started
	// is what this shows.
	it("shows a recording started from the CLI as active", async () => {
		serve({ lease: heldByUs, recording: recordingBody({ stepCount: 4 }), flows: [] });
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await waitFor(() => expect(recordButton()).toHaveAttribute("aria-pressed", "true"));
		expect(stepCount()).toBe("4");
	});

	it("says who is recording when it is another session, and refuses to touch it", async () => {
		serve({ lease: heldByUs, recording: recordingBody({ sessionId: "p-9", stepCount: 2 }), flows: [] });
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		// The reason is in the accessible name, not only in a tooltip a
		// disabled control cannot show to a keyboard.
		await waitFor(() => expect(recordButton()).toHaveAccessibleName(/@p-9 is recording this device/));
		expect(recordButton()).toBeDisabled();
		expect(recordButton()).toHaveAttribute("aria-pressed", "false");
	});

	it("is disabled with the reason when this session does not hold the lease", async () => {
		serve({ lease: heldByNobody, recording: noRecording, flows: [] });
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await waitFor(() => expect(recordButton()).toHaveAccessibleName(/Recording needs this session to hold the device/));
		expect(recordButton()).toBeDisabled();
		expect(postMock).not.toHaveBeenCalled();
	});

	it("a stopped recording reads as not recording", async () => {
		serve({
			lease: heldByUs,
			recording: recordingBody({ stoppedAt: "2026-08-18T05:00:00Z", stepCount: 9 }),
			flows: [],
		});
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		// ⚠ A stopped recording looks exactly like no recording, and BOTH look
		// exactly like the moment before the daemon has answered. Asserting
		// inside a waitFor passed on that loading window, so a bug that reported
		// every recording as open went unnoticed. Wait for the answer, then
		// assert - once, on a settled state.
		await waitFor(() =>
			expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/sim-recordings/{udid}", expect.anything()),
		);
		await waitFor(() => expect(recordButton()).toBeEnabled());
		expect(recordButton()).toHaveAccessibleName("Start recording");
		expect(stepCount()).toBe("rec");
	});
});

describe("the recordings list", () => {
	it("says how many recordings the task has, without being opened", async () => {
		serve({
			lease: heldByUs,
			recording: noRecording,
			flows: [flow(), flow({ fileName: "b-20260818-050000.000Z.yaml", name: "b" })],
		});
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		await waitFor(() => expect(screen.getByTestId("sim-recordings-count")).toHaveTextContent("2"));
		expect(screen.getByTestId("sim-recordings-trigger")).toHaveAccessibleName("Recordings - 2 in this session");
	});

	it("lists what is on disk: name, when, steps and how many need review", async () => {
		serve({ lease: heldByUs, recording: noRecording, flows: [flow()] });
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		const popover = await openRecordings();
		expect(within(popover).getByText("login-to-portfolio")).toBeInTheDocument();
		expect(within(popover).getByText("14 steps")).toBeInTheDocument();
		expect(within(popover).getByText("2 to review")).toBeInTheDocument();
	});

	// A flow recorded before flows stated their own counts is unmeasured, and
	// says so. "0 steps" for a flow with twelve of them is a number somebody
	// would act on.
	it("says the counts are unknown rather than showing zero", async () => {
		serve({ lease: heldByUs, recording: noRecording, flows: [flow({ countsKnown: false, steps: 0, review: 0 })] });
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		const popover = await openRecordings();
		expect(within(popover).getByText("steps unknown")).toBeInTheDocument();
		expect(within(popover).queryByText("0 steps")).not.toBeInTheDocument();
	});

	it("copies the exact path and the exact name, as separate affordances", async () => {
		const writeText = vi.fn().mockResolvedValue(undefined);
		window.ao!.clipboard.writeText = writeText;
		serve({ lease: heldByUs, recording: noRecording, flows: [flow()] });
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		const popover = await openRecordings();
		await userEvent.click(within(popover).getByRole("button", { name: /Copy recording path/ }));
		expect(writeText).toHaveBeenLastCalledWith("/data/sim/p-1/flows/login-to-portfolio-20260818-045722.711Z.yaml");

		await userEvent.click(within(popover).getByRole("button", { name: /Copy recording name/ }));
		expect(writeText).toHaveBeenLastCalledWith("login-to-portfolio");
	});

	it("names a recording afterwards, keeping the file it belongs to", async () => {
		serve({
			lease: heldByUs,
			recording: noRecording,
			flows: [flow({ name: "", fileName: "20260818-045722.711Z.yaml" })],
		});
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		const popover = await openRecordings();
		await userEvent.click(within(popover).getByRole("button", { name: /Rename/ }));
		const field = await screen.findByTestId("sim-flow-rename");
		await userEvent.type(field, "checkout journey{Enter}");

		expect(patchMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/sim-flows/{fileName}", {
			params: { path: { sessionId: "p-1", fileName: "20260818-045722.711Z.yaml" } },
			body: { name: "checkout journey" },
		});
	});

	// ⚠ Deleting costs replaying a path by hand. It is confirmed, it names what
	// goes, and it takes exactly one file.
	it("confirms before deleting, naming the file, and deletes only that one", async () => {
		serve({
			lease: heldByUs,
			recording: noRecording,
			flows: [flow(), flow({ fileName: "other-20260818-050000.000Z.yaml", name: "other" })],
		});
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		const popover = await openRecordings();
		await userEvent.click(within(popover).getByRole("button", { name: "Delete login-to-portfolio" }));
		expect(deleteMock).not.toHaveBeenCalled();
		expect(await screen.findByText(/login-to-portfolio-20260818-045722.711Z.yaml/)).toBeInTheDocument();
		expect(screen.getByText(/You would have to record it again/)).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Delete" }));
		expect(deleteMock).toHaveBeenCalledTimes(1);
		expect(deleteMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/sim-flows/{fileName}", {
			params: { path: { sessionId: "p-1", fileName: "login-to-portfolio-20260818-045722.711Z.yaml" } },
		});
	});

	it("cancelling the confirmation deletes nothing", async () => {
		serve({ lease: heldByUs, recording: noRecording, flows: [flow()] });
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		const popover = await openRecordings();
		await userEvent.click(within(popover).getByRole("button", { name: /^Delete login-to-portfolio$/ }));
		await userEvent.click(await screen.findByRole("button", { name: "Cancel" }));

		expect(deleteMock).not.toHaveBeenCalled();
		expect(await screen.findByText("login-to-portfolio")).toBeInTheDocument();
	});

	it("an empty list explains what to do rather than showing nothing", async () => {
		serve({ lease: heldByUs, recording: noRecording, flows: [] });
		render(<SimulatorPanel isActive sessionId="p-1" />, { wrapper });

		const popover = await openRecordings();
		expect(within(popover).getByText(/Press record, drive the device by hand, then stop/)).toBeInTheDocument();
	});
});
