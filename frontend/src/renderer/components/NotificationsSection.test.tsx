import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { NotificationsSection } from "./NotificationsSection";

const { showMock } = vi.hoisted(() => ({ showMock: vi.fn() }));

vi.mock("../lib/bridge", () => ({
	aoBridge: { notifications: { show: showMock } },
}));

beforeEach(() => {
	showMock.mockReset().mockResolvedValue(undefined);
});

describe("NotificationsSection", () => {
	it("posts a native notification down the real IPC path when the debug button is clicked", async () => {
		render(<NotificationsSection />);

		await userEvent.click(screen.getByRole("button", { name: "Send test notification" }));

		expect(showMock).toHaveBeenCalledTimes(1);
		expect(showMock).toHaveBeenCalledWith(
			expect.objectContaining({
				id: expect.stringMatching(/^test-notification-/),
				title: "Agent Orchestrator",
				body: expect.stringContaining("Test notification"),
			}),
		);
		expect(screen.getByText(/Look for a banner/)).toBeInTheDocument();
	});

	it("uses a fresh id per click so repeats are not collapsed", async () => {
		render(<NotificationsSection />);
		const button = screen.getByRole("button", { name: "Send test notification" });

		await userEvent.click(button);
		await userEvent.click(button);

		expect(showMock).toHaveBeenCalledTimes(2);
		const firstId = showMock.mock.calls[0][0].id;
		const secondId = showMock.mock.calls[1][0].id;
		expect(firstId).not.toBe(secondId);
	});
});
