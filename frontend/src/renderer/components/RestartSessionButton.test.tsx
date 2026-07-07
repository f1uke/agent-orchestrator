import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { RestartSessionButton } from "./RestartSessionButton";

const { postMock } = vi.hoisted(() => ({
	postMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		POST: postMock,
	},
	apiErrorMessage: (error: unknown, fallback = "Request failed") => {
		if (error instanceof Error) return error.message;
		if (typeof error === "object" && error !== null && "message" in error) {
			return String((error as { message: unknown }).message);
		}
		return fallback;
	},
}));

const worker: WorkspaceSession = {
	id: "sess-1",
	workspaceId: "proj-1",
	workspaceName: "my-app",
	title: "do the thing",
	provider: "claude-code",
	kind: "worker",
	branch: "ao/sess-1",
	status: "working",
	updatedAt: "2026-06-10T00:00:00Z",
	prs: [],
};

function renderButton(session: WorkspaceSession = worker) {
	const queryClient = new QueryClient({
		defaultOptions: {
			queries: { retry: false },
			mutations: { retry: false },
		},
	});
	render(
		<QueryClientProvider client={queryClient}>
			<RestartSessionButton session={session} />
		</QueryClientProvider>,
	);
	return queryClient;
}

beforeEach(() => {
	postMock.mockReset();
	postMock.mockResolvedValue({ data: { ok: true, sessionId: "sess-1" }, error: undefined });
});

describe("RestartSessionButton", () => {
	it("confirms before restarting, then posts to the restart endpoint", async () => {
		renderButton();

		await userEvent.click(screen.getByRole("button", { name: "Restart session" }));
		expect(postMock).not.toHaveBeenCalled();

		await userEvent.click(screen.getByRole("button", { name: "Restart" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/restart", {
			params: { path: { sessionId: "sess-1" } },
		});
	});

	it("can back out of the confirmation without restarting", async () => {
		renderButton();

		await userEvent.click(screen.getByRole("button", { name: "Restart session" }));
		await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

		expect(postMock).not.toHaveBeenCalled();
	});

	it("surfaces the daemon error when the restart fails", async () => {
		postMock.mockResolvedValue({ data: undefined, error: { message: "session not found" } });
		renderButton();

		await userEvent.click(screen.getByRole("button", { name: "Restart session" }));
		await userEvent.click(screen.getByRole("button", { name: "Restart" }));

		expect(await screen.findByText("session not found")).toBeInTheDocument();
	});
});
