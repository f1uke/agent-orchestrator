import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CrewMemberNotStartedPane } from "./CrewMemberNotStartedPane";
import type { WorkspaceSession } from "../types/workspace";

const post = vi.fn();
vi.mock("../lib/api-client", () => ({
	apiClient: {
		POST: (...args: unknown[]) => post(...args),
	},
	apiErrorMessage: (_e: unknown, fallback: string) => fallback,
}));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: () => Promise.resolve() }));

function qa(): WorkspaceSession {
	return {
		id: "demo-2",
		workspaceId: "demo",
		workspaceName: "Demo",
		title: "demo-2",
		provider: "claude-code",
		kind: "worker",
		branch: "feature/x",
		status: "idle",
		updatedAt: "2026-08-21T00:00:00Z",
		prs: [],
		isSuspended: true,
		crew: { id: "demo-1", role: "qa", hasRun: false },
	};
}

function renderPane() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<CrewMemberNotStartedPane session={qa()} />
		</QueryClientProvider>,
	);
}

describe("CrewMemberNotStartedPane", () => {
	// The pane this replaces said "Terminal ended" and offered to RESTORE a
	// session that had never run - death for an agent that has simply not been
	// started, and a verb for something there is nothing to restore.
	it("says the member has not started, not that its terminal ended", () => {
		renderPane();
		expect(screen.getByText(/qa · not started/i)).toBeInTheDocument();
		expect(screen.queryByText(/terminal ended/i)).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /restore/i })).not.toBeInTheDocument();
	});

	// The promise the baton bar used to make - "sleeps the other" - is gone, and
	// its replacement has to say the opposite out loud: both members work at once.
	it("promises that starting it does not interrupt dev", () => {
		renderPane();
		expect(screen.getByText(/does not interrupt dev/i)).toBeInTheDocument();
		expect(screen.getByText(/Nothing is spent until you start it/i)).toBeInTheDocument();
	});

	it("starts the member through the crew endpoint", async () => {
		post.mockResolvedValue({ error: undefined });
		renderPane();
		await userEvent.click(screen.getByRole("button", { name: /start qa/i }));
		await waitFor(() => expect(post).toHaveBeenCalled());
		expect(post.mock.calls[0][0]).toBe("/api/v1/sessions/{sessionId}/crew/wake");
		expect(post.mock.calls[0][1]).toEqual({ params: { path: { sessionId: "demo-2" } } });
	});

	it("shows why a start failed instead of leaving a dead button", async () => {
		post.mockResolvedValue({ error: { message: "boom" } });
		renderPane();
		await userEvent.click(screen.getByRole("button", { name: /start qa/i }));
		expect(await screen.findByText(/Unable to start qa/i)).toBeInTheDocument();
	});
});
