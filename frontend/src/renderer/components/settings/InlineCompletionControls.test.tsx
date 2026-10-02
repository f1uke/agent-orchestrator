import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InlineCompletionStatus } from "../../../main/inline-completion/service";
import { resetInlineCompletionStatusForTests } from "../../lib/inline-completion/status";
import { InlineCompletionControls, inlineCompletionSummary } from "./InlineCompletionControls";

const MODELS: InlineCompletionStatus["models"] = [
	{
		id: "sweep-next-edit-1.5b",
		kind: "next-edit",
		label: "Sweep Next-Edit 1.5B",
		blurb: "Finishes the line, then predicts the next edit.",
		sizeBytes: 1_537_269_856,
		installed: false,
	},
];

const OFF: InlineCompletionStatus = {
	unsupported: null,
	enabled: false,
	modelId: "sweep-next-edit-1.5b",
	server: "off",
	activeKind: null,
	activeInfill: false,
	serverDetail: null,
	pid: null,
	confirm: null,
	download: null,
	downloadError: null,
	models: MODELS,
};

let push: ((s: InlineCompletionStatus) => void) | null = null;
const bridge = {
	getStatus: vi.fn(),
	enable: vi.fn(),
	disable: vi.fn(),
	confirmDownload: vi.fn(),
	cancelDownload: vi.fn(),
	complete: vi.fn(),
	cancel: vi.fn(),
	onStatus: vi.fn(),
};

beforeEach(() => {
	resetInlineCompletionStatusForTests();
	for (const fn of Object.values(bridge)) fn.mockReset().mockResolvedValue(undefined);
	bridge.getStatus.mockResolvedValue(OFF);
	bridge.onStatus.mockImplementation((listener: (s: InlineCompletionStatus) => void) => {
		push = listener;
		return () => {
			push = null;
		};
	});
	(window as unknown as { ao: { inlineCompletion: typeof bridge } }).ao.inlineCompletion = bridge;
});

afterEach(() => {
	resetInlineCompletionStatusForTests();
});

const publish = (s: Partial<InlineCompletionStatus>) => act(() => push?.({ ...OFF, ...s }));

describe("InlineCompletionControls", () => {
	it("is off on a clean state, and turning it on asks main to enable", async () => {
		render(<InlineCompletionControls />);
		await waitFor(() => expect(screen.getByRole("switch")).not.toBeChecked());
		await userEvent.click(screen.getByRole("switch"));
		expect(bridge.enable).toHaveBeenCalled();
	});

	it("shows the download's size BEFORE it starts, and only Download starts it", async () => {
		render(<InlineCompletionControls />);
		await waitFor(() => expect(bridge.getStatus).toHaveBeenCalled());
		publish({
			confirm: {
				modelId: "sweep-next-edit-1.5b",
				bytes: 1_549_099_326,
				runtimeBytes: 11_829_470,
				freeBytes: 250 * 1024 ** 3,
			},
		});
		const confirm = await screen.findByTestId("inline-completion-confirm");
		expect(confirm).toHaveTextContent("Download Sweep Next-Edit 1.5B - 1.5 GB?");
		expect(confirm).toHaveTextContent("1.5 GB model and 12 MB llama.cpp runtime, into ~/.ao/llm");
		expect(confirm).toHaveTextContent("268.4 GB free");
		// The switch reads ON while the question it raised is open.
		expect(screen.getByRole("switch")).toBeChecked();
		expect(bridge.confirmDownload).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: "Download" }));
		expect(bridge.confirmDownload).toHaveBeenCalled();
	});

	it("shows progress with a cancel while downloading", async () => {
		render(<InlineCompletionControls />);
		await waitFor(() => expect(bridge.getStatus).toHaveBeenCalled());
		publish({
			enabled: true,
			download: {
				modelId: "sweep-next-edit-1.5b",
				label: "Sweep Next-Edit 1.5B",
				receivedBytes: 512 * 1024 ** 2,
				totalBytes: 1024 ** 3,
			},
		});
		expect(await screen.findByRole("progressbar")).toHaveAttribute("aria-valuenow", "50");
		expect(screen.getByTestId("inline-completion-download")).toHaveTextContent("537 MB / 1.1 GB");
		await userEvent.click(screen.getByRole("button", { name: "Cancel download" }));
		expect(bridge.cancelDownload).toHaveBeenCalled();
	});

	it("says ready, with the pid of the server AO started, and turning it off disables", async () => {
		render(<InlineCompletionControls />);
		await waitFor(() => expect(bridge.getStatus).toHaveBeenCalled());
		publish({ enabled: true, server: "ready", pid: 4242, serverDetail: "Sweep Next-Edit 1.5B" });
		const line = await screen.findByTestId("inline-completion-status");
		expect(line).toHaveTextContent("Ready - Sweep Next-Edit 1.5B");
		expect(line).toHaveTextContent("llama-server · pid 4242");
		await userEvent.click(screen.getByRole("switch"));
		expect(bridge.disable).toHaveBeenCalled();
	});

	it("an error carries its reason and a retry", async () => {
		render(<InlineCompletionControls />);
		await waitFor(() => expect(bridge.getStatus).toHaveBeenCalled());
		publish({ enabled: true, server: "error", serverDetail: "llama-server exited (code 3): error: out of memory" });
		expect(await screen.findByTestId("inline-completion-status")).toHaveTextContent("out of memory");
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(bridge.enable).toHaveBeenCalled();
	});

	it("an unsupported machine gets the reason instead of a switch", async () => {
		bridge.getStatus.mockResolvedValue({ ...OFF, unsupported: "Apple silicon Macs only." });
		render(<InlineCompletionControls />);
		expect(await screen.findByText("Apple silicon Macs only.")).toBeInTheDocument();
		expect(screen.queryByRole("switch")).toBeNull();
	});
});

describe("the model", () => {
	it("is said in one line beside the switch - there is one model, so there is no picker", async () => {
		render(<InlineCompletionControls />);
		expect(await screen.findByText("Finishes the line, then predicts the next edit.")).toBeInTheDocument();
		expect(screen.queryByRole("combobox")).toBeNull();
	});
});

describe("inlineCompletionSummary", () => {
	it("names each state in a word or two", () => {
		expect(inlineCompletionSummary(OFF)).toBe("Off");
		expect(inlineCompletionSummary({ ...OFF, enabled: true, server: "ready" })).toBe("On");
		expect(inlineCompletionSummary({ ...OFF, enabled: true, server: "starting" })).toBe("Starting");
		expect(
			inlineCompletionSummary({
				...OFF,
				download: { modelId: "sweep-next-edit-1.5b", label: "x", receivedBytes: 1, totalBytes: 4 },
			}),
		).toBe("Downloading 25%");
		expect(inlineCompletionSummary({ ...OFF, unsupported: "no" })).toBe("Unavailable");
		// The switch reads on while its question is open; the summary must agree.
		expect(
			inlineCompletionSummary({
				...OFF,
				confirm: { modelId: "sweep-next-edit-1.5b", bytes: 1, runtimeBytes: 0, freeBytes: null },
			}),
		).toBe("Needs download");
	});
});
