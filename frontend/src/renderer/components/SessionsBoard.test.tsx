import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";

const { navigateMock, workspaceQueryMock, deleteMock, postMock } = vi.hoisted(() => ({
	navigateMock: vi.fn(),
	workspaceQueryMock: vi.fn(),
	deleteMock: vi.fn(),
	postMock: vi.fn(),
}));

vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => navigateMock,
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: workspaceQueryMock,
	workspaceQueryKey: ["workspaces"],
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { DELETE: deleteMock, POST: postMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") => {
		if (error instanceof Error) return error.message;
		if (error && typeof error === "object" && "message" in error) return String((error as { message?: unknown }).message);
		return fallback;
	},
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
	postMock.mockReset();
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

	it("reopens a done session by restoring it", async () => {
		postMock.mockResolvedValue({ error: undefined });
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [doneSession("sess-1")] }],
			isError: false,
		});
		renderBoard();

		await userEvent.click(screen.getByRole("button", { name: /Done \/ Terminated/i }));
		await userEvent.click(screen.getByRole("button", { name: "Reopen session" }));

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/restore", {
				params: { path: { sessionId: "sess-1" } },
			}),
		);
	});

	it("treats an already-active merged session as reopened without surfacing an error", async () => {
		// A merged session still live on disk is not terminated, so restore is a no-op
		// (SESSION_NOT_RESTORABLE); the daemon auto-claims the newer PR behind the
		// scenes, so the chip must not show a failure.
		postMock.mockResolvedValue({ error: { code: "SESSION_NOT_RESTORABLE", message: "Session is not restorable" } });
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [{ ...doneSession("m1"), status: "merged" }] }],
			isError: false,
		});
		renderBoard();

		await userEvent.click(screen.getByRole("button", { name: /Done \/ Terminated/i }));
		await userEvent.click(screen.getByRole("button", { name: "Reopen session" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(screen.queryByText(/not restorable/i)).not.toBeInTheDocument();
		expect(screen.queryByText(/Reopen failed/i)).not.toBeInTheDocument();
	});

	it("shows no Reopen action once a session leaves the done bucket", () => {
		// After reopen, restore + auto-claim flip the session to an active status; it
		// then renders in a column, not the done bar, so its Reopen chip disappears.
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", sessions: [{ ...doneSession("sess-1"), status: "pr_open" }] }],
			isError: false,
		});
		renderBoard();

		expect(screen.queryByText(/Done \/ Terminated/i)).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Reopen session" })).not.toBeInTheDocument();
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
