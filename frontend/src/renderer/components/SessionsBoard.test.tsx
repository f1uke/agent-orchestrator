import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";

const { navigateMock, workspaceQueryMock, deleteMock } = vi.hoisted(() => ({
	navigateMock: vi.fn(),
	workspaceQueryMock: vi.fn(),
	deleteMock: vi.fn(),
}));

vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => navigateMock,
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: workspaceQueryMock,
	workspaceQueryKey: ["workspaces"],
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { DELETE: deleteMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") => (error instanceof Error ? error.message : fallback),
}));

import { SessionsBoard } from "./SessionsBoard";

function doneSession(id: string): WorkspaceSession {
	return {
		id,
		workspaceId: "proj-1",
		workspaceName: "my-app",
		title: `finished ${id}`,
		provider: "claude-code",
		kind: "worker",
		branch: `ao/${id}`,
		status: "terminated",
		updatedAt: "2026-06-10T00:00:00Z",
		prs: [],
	};
}

function renderBoard() {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	render(
		<QueryClientProvider client={queryClient}>
			<SessionsBoard />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	navigateMock.mockReset();
	deleteMock.mockReset();
	workspaceQueryMock.mockReset().mockReturnValue({ data: [], isError: false });
});

describe("SessionsBoard", () => {
	it("does not show an agent setup warning on the board", () => {
		renderBoard();

		expect(screen.queryByText(/reload agents/i)).not.toBeInTheDocument();
	});

	it("deletes a done session after confirm", async () => {
		deleteMock.mockResolvedValue({ error: undefined });
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [doneSession("sess-1")] }],
			isError: false,
		});
		renderBoard();

		await userEvent.click(screen.getByRole("button", { name: /Done \/ Terminated/i }));
		await userEvent.click(screen.getByRole("button", { name: "Delete session" }));
		expect(deleteMock).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));

		await waitFor(() =>
			expect(deleteMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}", {
				params: { path: { sessionId: "sess-1" }, query: { force: false } },
			}),
		);
	});

	it("clears all done sessions", async () => {
		deleteMock.mockResolvedValue({ error: undefined });
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [doneSession("s1"), doneSession("s2")] }],
			isError: false,
		});
		renderBoard();

		await userEvent.click(screen.getByRole("button", { name: /Done \/ Terminated/i }));
		await userEvent.click(screen.getByRole("button", { name: "Clear all" }));
		await userEvent.click(screen.getByRole("button", { name: "Delete all" }));

		await waitFor(() => expect(deleteMock).toHaveBeenCalledTimes(2));
	});
});
