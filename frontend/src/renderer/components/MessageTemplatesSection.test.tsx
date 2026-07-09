import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, putMock, deleteMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn(), deleteMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, PUT: putMock, DELETE: deleteMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { MessageTemplatesSection } from "./MessageTemplatesSection";

function renderSection() {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<MessageTemplatesSection />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({
		data: {
			templates: [
				{ name: "review-comment-dispatch", default: "DEFAULT RCD", placeholders: ["{{.Comments}}"], override: null },
				{ name: "ci-failing", default: "DEFAULT CI", placeholders: ["{{.LogTail}}"], override: "CUSTOM CI" },
				// Zero-placeholder row (mirrors the real merge-conflict template):
				// exercises the render path with an empty placeholders array so a
				// regression back to a null/undefined placeholders field would
				// crash this render instead of only surfacing in production.
				{ name: "merge-conflict", default: "DEFAULT MC", placeholders: null as unknown as string[], override: null },
			],
		},
		error: undefined,
	});
	putMock.mockReset().mockResolvedValue({ data: { templates: [] }, error: undefined });
	deleteMock.mockReset().mockResolvedValue({ data: { templates: [] }, error: undefined });
});

describe("MessageTemplatesSection", () => {
	it("prefills override else default and saves an edit", async () => {
		renderSection();
		const ci = (await screen.findByLabelText(/ci-failing/i)) as HTMLTextAreaElement;
		await waitFor(() => expect(ci.value).toBe("CUSTOM CI"));
		await userEvent.clear(ci);
		await userEvent.type(ci, "NEW CI");
		await userEvent.click(screen.getAllByRole("button", { name: /save/i })[1]);
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/message-templates/{name}", {
				params: { path: { name: "ci-failing" } },
				body: { template: "NEW CI" },
			}),
		);
	});

	it("reset is disabled without an override and calls DELETE when present", async () => {
		renderSection();
		const resets = await screen.findAllByRole("button", { name: /reset to default/i });
		expect(resets[0]).toBeDisabled(); // review-comment-dispatch has no override
		expect(resets[1]).toBeEnabled(); // ci-failing has an override
		await userEvent.click(resets[1]);
		await waitFor(() =>
			expect(deleteMock).toHaveBeenCalledWith("/api/v1/settings/message-templates/{name}", {
				params: { path: { name: "ci-failing" } },
			}),
		);
	});

	it("preserves an unsaved edit in one template when another is saved and refetched", async () => {
		let getCallCount = 0;
		getMock.mockReset().mockImplementation(async () => {
			getCallCount += 1;
			const ciOverride = getCallCount === 1 ? "CUSTOM CI" : "NEW CI";
			return {
				data: {
					templates: [
						{ name: "review-comment-dispatch", default: "DEFAULT RCD", placeholders: ["{{.Comments}}"], override: null },
						{ name: "ci-failing", default: "DEFAULT CI", placeholders: ["{{.LogTail}}"], override: ciOverride },
					],
				},
				error: undefined,
			};
		});
		putMock.mockReset().mockResolvedValue({ data: { templates: [] }, error: undefined });

		renderSection();
		const rcd = (await screen.findByLabelText(/review-comment-dispatch/i)) as HTMLTextAreaElement;
		const ci = (await screen.findByLabelText(/ci-failing/i)) as HTMLTextAreaElement;
		await waitFor(() => expect(ci.value).toBe("CUSTOM CI"));

		// Edit both templates, but only save ci-failing.
		await userEvent.clear(rcd);
		await userEvent.type(rcd, "UNSAVED RCD EDIT");
		await userEvent.clear(ci);
		await userEvent.type(ci, "NEW CI");

		await userEvent.click(screen.getAllByRole("button", { name: /save/i })[1]);
		await waitFor(() => expect(putMock).toHaveBeenCalled());
		// Wait for the post-invalidation refetch to complete.
		await waitFor(() => expect(getCallCount).toBeGreaterThan(1));

		// Saved template syncs to its new server value; the untouched-save
		// template keeps the user's unsaved edit instead of being wiped by the
		// refetch.
		await waitFor(() => expect(ci.value).toBe("NEW CI"));
		expect(rcd.value).toBe("UNSAVED RCD EDIT");
	});
});
