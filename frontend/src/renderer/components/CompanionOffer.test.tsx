import { describe, expect, it, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CompanionOffer } from "./CompanionOffer";

const { get, set } = vi.hoisted(() => ({ get: vi.fn(), set: vi.fn() }));

vi.mock("../lib/bridge", () => ({ aoBridge: { companionSettings: { get, set } } }));

beforeEach(() => {
	get.mockReset();
	set.mockReset().mockResolvedValue(undefined);
});

function renderOffer() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<CompanionOffer />
		</QueryClientProvider>,
	);
}

describe("CompanionOffer", () => {
	it("offers the companion once, on the first run", async () => {
		get.mockResolvedValue({ enabled: false, asked: false });
		renderOffer();

		expect(await screen.findByRole("dialog")).toBeInTheDocument();
	});

	it("stays out of the way once the question has been answered", async () => {
		get.mockResolvedValue({ enabled: false, asked: true });
		renderOffer();

		await waitFor(() => expect(get).toHaveBeenCalled());
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	});

	it("turns the companion on when the offer is accepted", async () => {
		get.mockResolvedValue({ enabled: false, asked: false });
		renderOffer();

		await userEvent.click(await screen.findByRole("button", { name: /show the companion/i }));

		expect(set).toHaveBeenCalledWith({ enabled: true, asked: true });
	});

	it("records the decline so it is never asked again", async () => {
		get.mockResolvedValue({ enabled: false, asked: false });
		renderOffer();

		await userEvent.click(await screen.findByRole("button", { name: /no thanks/i }));

		expect(set).toHaveBeenCalledWith({ enabled: false, asked: true });
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	});
});
