import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { postMock } = vi.hoisted(() => ({ postMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { POST: postMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { SendToWorkerButton } from "./SendToWorkerButton";

function renderButton() {
	const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<SendToWorkerButton sessionId="s1" prUrl="https://gh/pr/1" threadId="T1" />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	postMock.mockReset().mockResolvedValue({ data: { ok: true, sessionId: "s1" }, error: undefined });
});

describe("SendToWorkerButton", () => {
	it("sends the thread with no extra instructions on the primary click and shows a sent state", async () => {
		const user = userEvent.setup();
		renderButton();

		await user.click(screen.getByRole("button", { name: "Send to worker" }));

		expect(postMock).toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/comment-dispatch",
			expect.objectContaining({
				params: { path: { sessionId: "s1" } },
				body: { prUrl: "https://gh/pr/1", threadId: "T1", extraPrompt: "" },
			}),
		);
		expect(await screen.findByText(/sent/i)).toBeInTheDocument();
	});

	it("opens the panel, sends typed extra instructions, and clears the textarea", async () => {
		const user = userEvent.setup();
		renderButton();

		await user.click(screen.getByRole("button", { name: /add extra instructions/i }));
		const textarea = screen.getByLabelText(/extra instructions for the worker/i);
		await user.type(textarea, "also check the other call sites");
		await user.click(screen.getByRole("button", { name: /send with instructions/i }));

		expect(postMock).toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/comment-dispatch",
			expect.objectContaining({
				params: { path: { sessionId: "s1" } },
				body: { prUrl: "https://gh/pr/1", threadId: "T1", extraPrompt: "also check the other call sites" },
			}),
		);

		// panel closes and the textarea clears on success
		await screen.findByText(/sent/i);
		expect(screen.queryByLabelText(/extra instructions for the worker/i)).not.toBeInTheDocument();
	});
});
