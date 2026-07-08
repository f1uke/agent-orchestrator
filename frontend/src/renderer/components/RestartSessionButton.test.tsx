import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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

	it("does not pull focus back to the restart trigger when the confirm dialog is dismissed by an outside pointer press", async () => {
		const user = userEvent.setup();
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});
		render(
			<QueryClientProvider client={queryClient}>
				<RestartSessionButton session={worker} />
				{/* Terminal stand-in: grabs focus on pointer-down like xterm does. */}
				<div data-testid="terminal" onPointerDown={(event) => event.currentTarget.focus()} tabIndex={-1}>
					terminal
				</div>
			</QueryClientProvider>,
		);

		const trigger = screen.getByRole("button", { name: "Restart session" });
		await user.click(trigger);
		expect(await screen.findByRole("dialog")).toBeInTheDocument();

		// An outside pointer press closes the dialog; the guard keeps focus on what
		// was pressed instead of yanking it back to the trigger (which would leave a
		// stray focus ring), so a single click both dismisses and lands where meant.
		const terminal = screen.getByTestId("terminal");
		fireEvent.pointerDown(terminal);
		fireEvent.pointerUp(terminal);

		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(trigger).not.toHaveFocus();
	});

	it("returns focus to the restart trigger when the confirm dialog is closed with Escape (keyboard accessibility)", async () => {
		const user = userEvent.setup();
		renderButton();

		const trigger = screen.getByRole("button", { name: "Restart session" });
		await user.click(trigger);
		expect(await screen.findByRole("dialog")).toBeInTheDocument();

		await user.keyboard("{Escape}");

		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(trigger).toHaveFocus();
	});

	it("surfaces the daemon error when the restart fails", async () => {
		postMock.mockResolvedValue({ data: undefined, error: { message: "session not found" } });
		renderButton();

		await userEvent.click(screen.getByRole("button", { name: "Restart session" }));
		await userEvent.click(screen.getByRole("button", { name: "Restart" }));

		expect(await screen.findByText("session not found")).toBeInTheDocument();
	});
});
