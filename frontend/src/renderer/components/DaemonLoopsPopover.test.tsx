import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "./ui/tooltip";
import type { DaemonLoop } from "../hooks/useDaemonLoops";

const useDaemonLoopsMock = vi.fn();
vi.mock("../hooks/useDaemonLoops", () => ({
	useDaemonLoops: (...args: unknown[]) => useDaemonLoopsMock(...args),
}));

// Imported after the mock is registered.
import { DaemonLoopsPopover } from "./DaemonLoopsPopover";

function renderPopover() {
	return render(
		<TooltipProvider>
			<DaemonLoopsPopover open />
		</TooltipProvider>,
	);
}

const ranLoop: DaemonLoop = {
	name: "scm-observer",
	displayName: "PR / CI polling",
	description: "Polls each session's PR for changes.",
	intervalMs: 30_000,
	lastRunAt: new Date(Date.now() - 5_000).toISOString(),
	nextRunAt: new Date(Date.now() + 25_000).toISOString(),
	running: true,
};

const neverRunLoop: DaemonLoop = {
	name: "idle-sweep",
	displayName: "Auto-close idle",
	description: "Closes idle sessions past their TTL.",
	intervalMs: 300_000,
	running: true,
};

describe("DaemonLoopsPopover", () => {
	it("renders a row per loop with a countdown and a never-run state", () => {
		useDaemonLoopsMock.mockReturnValue({ data: [ranLoop, neverRunLoop], isLoading: false, isError: false });
		renderPopover();
		expect(screen.getByText("PR / CI polling")).toBeTruthy();
		expect(screen.getByText("Auto-close idle")).toBeTruthy();
		expect(screen.getByText(/next in/i)).toBeTruthy();
		expect(screen.getByText(/waiting for first run/i)).toBeTruthy();
	});

	it("shows the offline state when the loops request fails", () => {
		useDaemonLoopsMock.mockReturnValue({ data: undefined, isLoading: false, isError: true });
		renderPopover();
		expect(screen.getByText("Daemon offline")).toBeTruthy();
	});

	it("shows an empty state when there are no loops", () => {
		useDaemonLoopsMock.mockReturnValue({ data: [], isLoading: false, isError: false });
		renderPopover();
		expect(screen.getByText("No loops running")).toBeTruthy();
	});
});
