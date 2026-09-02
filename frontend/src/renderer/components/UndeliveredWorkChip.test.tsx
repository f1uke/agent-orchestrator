import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";

const { postMock } = vi.hoisted(() => ({ postMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { POST: postMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") => {
		if (error && typeof error === "object" && "message" in error)
			return String((error as { message?: unknown }).message);
		return fallback;
	},
}));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));
vi.mock("../hooks/useWorkspaceQuery", () => ({ workspaceQueryKey: ["workspaces"] }));

import { UndeliveredWorkChip } from "./UndeliveredWorkChip";

function parked(): WorkspaceSession {
	return {
		id: "sess-60",
		workspaceId: "proj-1",
		workspaceName: "my-app",
		title: "article webview zoom",
		provider: "claude-code",
		kind: "worker",
		branch: "feature/zoom",
		status: "needs_input",
		updatedAt: "2026-09-02T00:00:00Z",
		isSuspended: true,
		sleepReason: "undelivered",
		prs: [],
	};
}

const refusal = {
	error: {
		error: "conflict",
		code: "SESSION_HAS_UNDELIVERED_WORK",
		message: "sess-60 still holds 2 uncommitted files that no pull request carries",
		details: {
			reason: "workspace_dirty",
			files: [
				{ path: "Sources/Article/WebViewZoom.swift", status: "modified" },
				{ path: "Sources/Article/NewFile.swift", status: "untracked" },
			],
		},
	},
};

function renderChip() {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={qc}>
			<UndeliveredWorkChip session={parked()} />
		</QueryClientProvider>,
	);
}

describe("UndeliveredWorkChip", () => {
	beforeEach(() => {
		postMock.mockReset();
		postMock.mockResolvedValue({ data: { ok: true, sessionId: "sess-60", terminated: true, freed: true } });
	});

	// The silence is the bug. Before anyone clicks anything, the card has to say
	// that this session is holding work - otherwise it looks like every other
	// card that refuses to move.
	it("says the card is holding undelivered work without being asked", () => {
		renderChip();
		expect(screen.getByText("Undelivered")).toBeInTheDocument();
	});

	// The refusal is the explanation. A 409 must open the dialog with the file
	// list, not vanish into a closed menu the way the 200 used to.
	it("explains the refusal with the files that caused it", async () => {
		postMock.mockResolvedValue(refusal);
		renderChip();

		await userEvent.click(screen.getByRole("button", { name: "Move to Done" }));

		expect(await screen.findByText(/still holds undelivered work/i)).toBeInTheDocument();
		expect(screen.getByText("Sources/Article/WebViewZoom.swift")).toBeInTheDocument();
		expect(screen.getByText("Sources/Article/NewFile.swift")).toBeInTheDocument();
		expect(screen.getByText("untracked")).toBeInTheDocument();
		// Both ways out are offered, and the destructive one says so.
		expect(screen.getByRole("button", { name: /Discard and move to Done/i })).toBeInTheDocument();
	});

	// And the deliberate answer goes through, with the opt-in the daemon requires.
	it("discards deliberately once the human has seen the list", async () => {
		postMock.mockResolvedValueOnce(refusal);
		renderChip();
		await userEvent.click(screen.getByRole("button", { name: "Move to Done" }));
		await screen.findByText("Sources/Article/NewFile.swift");

		postMock.mockResolvedValue({
			data: { ok: true, sessionId: "sess-60", terminated: true, freed: true, preservedRef: "refs/ao/preserved/sess-60" },
		});
		await userEvent.click(screen.getByRole("button", { name: /Discard and move to Done/i }));

		await waitFor(() =>
			expect(postMock).toHaveBeenLastCalledWith("/api/v1/sessions/{sessionId}/kill", {
				params: { path: { sessionId: "sess-60" } },
				body: { discardUncommitted: true },
			}),
		);
	});

	// A parked session whose tree is clean is not in the way at all: one click
	// ends it, and nothing asks the human about files that do not exist.
	it("moves a clean parked session straight to Done", async () => {
		renderChip();
		await userEvent.click(screen.getByRole("button", { name: "Move to Done" }));

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/kill", {
				params: { path: { sessionId: "sess-60" } },
				body: { discardUncommitted: false },
			}),
		);
		expect(screen.queryByText(/still holds undelivered work/i)).not.toBeInTheDocument();
	});
});
