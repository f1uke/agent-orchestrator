import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, PUT: putMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { SpawnConfirmSection } from "./SpawnConfirmSection";

function renderSection() {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<SpawnConfirmSection />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({ data: { enabled: true }, error: undefined });
	putMock.mockReset().mockResolvedValue({ data: { enabled: false }, error: undefined });
});

describe("SpawnConfirmSection", () => {
	it("loads the setting and saves a toggle to off", async () => {
		renderSection();
		const select = await screen.findByLabelText(/confirm before spawning/i);
		// Wait for the loaded value to seed the control before toggling it.
		await waitFor(() => expect(select).toHaveTextContent(/enabled/i));
		await userEvent.click(select);
		await userEvent.click(await screen.findByRole("option", { name: /disabled/i }));
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/spawn-confirm", {
				body: { enabled: false },
			}),
		);
	});
});
