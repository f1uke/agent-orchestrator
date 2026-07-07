import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, PUT: putMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { AutoReclaimSection } from "./AutoReclaimSection";

function renderSection() {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<AutoReclaimSection />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({ data: { enabled: true, graceMinutes: 15 }, error: undefined });
	putMock.mockReset().mockResolvedValue({ data: { enabled: true, graceMinutes: 20 }, error: undefined });
});

describe("AutoReclaimSection", () => {
	it("loads settings and saves an edited grace", async () => {
		renderSection();
		const input = await screen.findByLabelText(/grace/i);
		// Wait for the loaded value to actually seed the input before editing it:
		// the settings query resolves asynchronously, and the effect that seeds
		// `form` from `query.data` can otherwise land *after* clear()/type() have
		// already started, clobbering the in-progress edit back to the loaded value.
		await waitFor(() => expect(input).toHaveValue(15));
		await userEvent.clear(input);
		await userEvent.type(input, "20");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/reclaim", {
				body: { enabled: true, graceMinutes: 20 },
			}),
		);
	});
});
