import { describe, expect, it, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CompanionControls } from "./CompanionControls";

const { get, set } = vi.hoisted(() => ({ get: vi.fn(), set: vi.fn() }));

vi.mock("../../lib/bridge", () => ({ aoBridge: { companionSettings: { get, set } } }));

beforeEach(() => {
	get.mockReset().mockResolvedValue({ enabled: false, asked: true });
	set.mockReset().mockResolvedValue(undefined);
});

function renderControls() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<CompanionControls />
		</QueryClientProvider>,
	);
}

describe("CompanionControls", () => {
	it("shows the companion as off until the user turns it on", async () => {
		renderControls();

		await waitFor(() => expect(screen.getByRole("switch")).not.toBeChecked());
	});

	it("turns the companion on, keeping the answered-the-offer flag", async () => {
		renderControls();
		await waitFor(() => expect(get).toHaveBeenCalled());

		await userEvent.click(screen.getByRole("switch"));

		expect(set).toHaveBeenCalledWith({ enabled: true, asked: true });
	});

	it("turns it off again", async () => {
		get.mockResolvedValue({ enabled: true, asked: true });
		renderControls();
		await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());

		await userEvent.click(screen.getByRole("switch"));

		expect(set).toHaveBeenCalledWith({ enabled: false, asked: true });
	});

	it("marks the offer answered when the switch is used before it was ever asked", async () => {
		// Flipping the switch IS an answer, so the first-run dialog must not appear
		// afterwards asking a question the user has already settled.
		get.mockResolvedValue({ enabled: false, asked: false });
		renderControls();
		await waitFor(() => expect(get).toHaveBeenCalled());

		await userEvent.click(screen.getByRole("switch"));

		expect(set).toHaveBeenCalledWith({ enabled: true, asked: true });
	});
});
