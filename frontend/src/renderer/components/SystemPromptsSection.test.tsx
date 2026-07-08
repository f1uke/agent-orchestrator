import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, putMock, deleteMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn(), deleteMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, PUT: putMock, DELETE: deleteMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { SystemPromptsSection } from "./SystemPromptsSection";

function renderSection() {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<SystemPromptsSection />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({
		data: {
			prompts: [
				{ kind: "orchestrator", default: "ORCH DEFAULT {{.ProjectID}}", override: null },
				{ kind: "worker", default: "WORKER DEFAULT", override: "WORKER OVERRIDE" },
				{ kind: "reviewer", default: "REVIEWER DEFAULT", override: null },
			],
		},
		error: undefined,
	});
	putMock.mockReset().mockResolvedValue({ data: { prompts: [] }, error: undefined });
	deleteMock.mockReset().mockResolvedValue({ data: { prompts: [] }, error: undefined });
});

describe("SystemPromptsSection", () => {
	it("prefills each kind with override else default and saves an edit", async () => {
		renderSection();
		const worker = (await screen.findByLabelText(/worker/i)) as HTMLTextAreaElement;
		await waitFor(() => expect(worker.value).toBe("WORKER OVERRIDE"));
		await userEvent.clear(worker);
		await userEvent.type(worker, "NEW WORKER");
		await userEvent.click(screen.getAllByRole("button", { name: /save/i })[1]);
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/prompts/{kind}", {
				params: { path: { kind: "worker" } },
				body: { base: "NEW WORKER" },
			}),
		);
	});

	it("resets a kind to default via DELETE, disabled when no override", async () => {
		renderSection();
		// orchestrator has no override → its Reset is disabled.
		const resets = await screen.findAllByRole("button", { name: /reset to default/i });
		expect(resets[0]).toBeDisabled();
		// worker has an override → Reset enabled.
		expect(resets[1]).toBeEnabled();
		await userEvent.click(resets[1]);
		await waitFor(() =>
			expect(deleteMock).toHaveBeenCalledWith("/api/v1/settings/prompts/{kind}", {
				params: { path: { kind: "worker" } },
			}),
		);
	});

	it("preserves an unsaved edit in one kind when another kind is saved and refetched", async () => {
		let getCallCount = 0;
		getMock.mockReset().mockImplementation(async () => {
			getCallCount += 1;
			const workerOverride = getCallCount === 1 ? "WORKER OVERRIDE" : "NEW WORKER";
			return {
				data: {
					prompts: [
						{ kind: "orchestrator", default: "ORCH DEFAULT {{.ProjectID}}", override: null },
						{ kind: "worker", default: "WORKER DEFAULT", override: workerOverride },
						{ kind: "reviewer", default: "REVIEWER DEFAULT", override: null },
					],
				},
				error: undefined,
			};
		});
		putMock.mockReset().mockResolvedValue({ data: { prompts: [] }, error: undefined });

		renderSection();
		const worker = (await screen.findByLabelText(/worker/i)) as HTMLTextAreaElement;
		const reviewer = (await screen.findByLabelText(/reviewer/i)) as HTMLTextAreaElement;
		await waitFor(() => expect(worker.value).toBe("WORKER OVERRIDE"));

		// Edit both worker and reviewer, but only save worker.
		await userEvent.clear(worker);
		await userEvent.type(worker, "NEW WORKER");
		await userEvent.clear(reviewer);
		await userEvent.type(reviewer, "UNSAVED REVIEWER EDIT");

		await userEvent.click(screen.getAllByRole("button", { name: /save/i })[1]);
		await waitFor(() => expect(putMock).toHaveBeenCalled());
		// Wait for the post-invalidation refetch to complete.
		await waitFor(() => expect(getCallCount).toBeGreaterThan(1));

		// Saved kind syncs to its new server value; the untouched-save kind keeps
		// the user's unsaved edit instead of being wiped by the refetch.
		await waitFor(() => expect(worker.value).toBe("NEW WORKER"));
		expect(reviewer.value).toBe("UNSAVED REVIEWER EDIT");
	});
});
