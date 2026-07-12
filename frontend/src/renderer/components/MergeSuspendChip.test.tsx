import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { PRState, WorkspaceSession } from "../types/workspace";

const { postMock } = vi.hoisted(() => ({ postMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { POST: postMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") => {
		if (error instanceof Error) return error.message;
		if (error && typeof error === "object" && "message" in error)
			return String((error as { message?: unknown }).message);
		return fallback;
	},
}));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));
vi.mock("../hooks/useWorkspaceQuery", () => ({ workspaceQueryKey: ["workspaces"] }));

import { MergeSuspendChip } from "./MergeSuspendChip";

function mergeSuspended(prNumbers: Array<[number, PRState]> = [[12, "merged"]]): WorkspaceSession {
	return {
		id: "sess-9",
		workspaceId: "proj-1",
		workspaceName: "my-app",
		title: "shipped one",
		provider: "claude-code",
		kind: "worker",
		branch: "feat/x",
		status: "needs_input",
		updatedAt: "2026-06-10T00:00:00Z",
		isSuspended: true,
		keepWarmOnMerge: true,
		prs: prNumbers.map(([number, state]) => ({
			url: `u/${number}`,
			number,
			state,
			ci: "passing",
			review: "approved",
			mergeability: "mergeable",
			reviewComments: false,
			updatedAt: "2026-06-10T00:00:00Z",
		})),
	};
}

function renderChip(session = mergeSuspended()) {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={qc}>
			<MergeSuspendChip session={session} />
		</QueryClientProvider>,
	);
}

describe("MergeSuspendChip", () => {
	beforeEach(() => {
		postMock.mockReset();
		postMock.mockResolvedValue({ error: undefined });
	});

	it("labels the highest merged PR number", () => {
		renderChip(
			mergeSuspended([
				[3, "merged"],
				[9, "merged"],
				[5, "closed"],
			]),
		);
		expect(screen.getByText("Merged #9")).toBeInTheDocument();
	});

	it("has a single Move to Done action (no Continue button — opening the card resumes)", () => {
		renderChip();
		expect(screen.getByRole("button", { name: "Move to Done" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Continue" })).not.toBeInTheDocument();
	});

	it("Move to Done POSTs /kill for the session (archive to Done)", async () => {
		renderChip();
		await userEvent.click(screen.getByRole("button", { name: "Move to Done" }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/kill", {
				params: { path: { sessionId: "sess-9" } },
			}),
		);
	});

	it("surfaces an error in the title when the action fails", async () => {
		postMock.mockResolvedValue({ error: { message: "boom" } });
		renderChip();
		await userEvent.click(screen.getByRole("button", { name: "Move to Done" }));
		await waitFor(() =>
			expect(screen.getByLabelText("Merged #12 — open to continue, or move to Done")).toHaveAttribute("title", "boom"),
		);
	});
});
