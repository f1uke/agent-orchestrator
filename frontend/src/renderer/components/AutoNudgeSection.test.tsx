import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, PUT: putMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { AutoNudgeSection } from "./AutoNudgeSection";

function renderSection() {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<AutoNudgeSection />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({ data: { enabled: false }, error: undefined });
	putMock.mockReset().mockResolvedValue({ data: { enabled: true }, error: undefined });
});

describe("AutoNudgeSection", () => {
	it("renders the current enabled state from the GET", async () => {
		renderSection();
		const toggle = await screen.findByLabelText(/enabled by default/i);
		await waitFor(() => expect(toggle).toHaveAttribute("data-state", "unchecked"));
	});

	it("saves immediately on toggle, PUTting the negated value", async () => {
		renderSection();
		const toggle = await screen.findByLabelText(/enabled by default/i);
		await waitFor(() => expect(toggle).toHaveAttribute("data-state", "unchecked"));

		await userEvent.click(toggle);

		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/auto-nudge", {
				body: { enabled: true },
			}),
		);
	});
});
