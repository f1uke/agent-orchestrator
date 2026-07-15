import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TodoSpecEditor } from "./TodoSpecEditor";
import type { WorkspaceSession } from "../types/workspace";

const { getMock, postMock, patchMock, deleteMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	patchMock: vi.fn(),
	deleteMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: (...args: unknown[]) => getMock(...args),
		POST: (...args: unknown[]) => postMock(...args),
		PATCH: (...args: unknown[]) => patchMock(...args),
		DELETE: (...args: unknown[]) => deleteMock(...args),
	},
	apiErrorMessage: (_error: unknown, fallback = "Request failed") => fallback,
}));

const todo: WorkspaceSession = {
	id: "proj-1-72",
	workspaceId: "proj-1",
	workspaceName: "proj-1",
	title: "settings-migration",
	provider: "codex",
	kind: "worker",
	branch: "chore/settings-store",
	status: "todo",
	isTodo: true,
	baseBranch: "main-fluke",
	prTarget: "main-fluke",
	autoNameBranch: false,
	createdBy: "proj-1-orchestrator",
	prompt: "Migrate the autonudge setting into its own store.",
	updatedAt: new Date().toISOString(),
	prs: [],
};

function renderEditor(props: Partial<Parameters<typeof TodoSpecEditor>[0]> = {}) {
	const onStarted = vi.fn();
	const onDeleted = vi.fn();
	render(
		<QueryClientProvider client={new QueryClient()}>
			<TodoSpecEditor session={todo} onStarted={onStarted} onDeleted={onDeleted} {...props} />
		</QueryClientProvider>,
	);
	return { onStarted, onDeleted };
}

beforeEach(() => {
	getMock.mockReset().mockImplementation(async (path: string) => {
		if (path === "/api/v1/agents") {
			return {
				data: {
					supported: [{ id: "codex", label: "Codex" }],
					installed: [{ id: "codex", label: "Codex", authStatus: "authorized" }],
					authorized: [{ id: "codex", label: "Codex", authStatus: "authorized" }],
				},
				error: undefined,
			};
		}
		if (path === "/api/v1/projects/{id}/branches") {
			return { data: { branches: ["main-fluke", "main"] }, error: undefined };
		}
		return { data: undefined, error: undefined };
	});
	postMock.mockReset().mockResolvedValue({ data: { session: { id: "proj-1-72" } }, error: undefined });
	patchMock.mockReset().mockResolvedValue({ data: undefined, error: undefined });
	deleteMock.mockReset().mockResolvedValue({ data: undefined, error: undefined });
});

afterEach(() => vi.restoreAllMocks());

describe("TodoSpecEditor", () => {
	it("renders the TODO spec fields from the session", () => {
		renderEditor();
		expect(screen.getByText("proj-1-72")).toBeInTheDocument();
		expect(screen.getByText(/TODO · not started/)).toBeInTheDocument();
		expect(screen.getByLabelText("Task name")).toHaveValue("settings-migration");
		expect(screen.getByText("Migrate the autonudge setting into its own store.")).toBeInTheDocument();
	});

	it("autosaves an edited field via PATCH /spec on blur", async () => {
		renderEditor();
		const user = userEvent.setup();

		const name = screen.getByLabelText("Task name");
		await user.clear(name);
		await user.type(name, "renamed-task");
		await user.tab();

		await waitFor(() => expect(patchMock).toHaveBeenCalledTimes(1));
		expect(patchMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/spec", {
			params: { path: { sessionId: "proj-1-72" } },
			body: expect.objectContaining({ displayName: "renamed-task" }),
		});
	});

	it("does not PATCH when a field is committed unchanged", async () => {
		renderEditor();
		const user = userEvent.setup();
		await user.click(screen.getByLabelText("Task name"));
		await user.tab();
		expect(patchMock).not.toHaveBeenCalled();
	});

	it("starts the task via POST /start and reports the started id", async () => {
		const { onStarted } = renderEditor();
		const user = userEvent.setup();

		await user.click(screen.getByRole("button", { name: /Start work/ }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/start", {
			params: { path: { sessionId: "proj-1-72" } },
		});
		await waitFor(() => expect(onStarted).toHaveBeenCalledWith("proj-1-72"));
	});

	it("persists a pending edit before starting", async () => {
		renderEditor();
		const user = userEvent.setup();

		const name = screen.getByLabelText("Task name");
		await user.clear(name);
		await user.type(name, "edited-then-started");
		// Start without blurring first — the mutation must flush the draft.
		await user.click(screen.getByRole("button", { name: /Start work/ }));

		await waitFor(() => expect(patchMock).toHaveBeenCalled());
		await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/start", expect.anything()));
	});

	it("deletes the task after an inline confirm and fires onDeleted", async () => {
		const { onDeleted } = renderEditor();
		const user = userEvent.setup();

		await user.click(screen.getByRole("button", { name: /^Delete$/ }));
		await user.click(screen.getByRole("button", { name: "Delete task" }));

		await waitFor(() => expect(deleteMock).toHaveBeenCalledTimes(1));
		expect(deleteMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}", {
			params: { path: { sessionId: "proj-1-72" }, query: { force: true } },
		});
		await waitFor(() => expect(onDeleted).toHaveBeenCalled());
	});

	it("shows a Reset control once edited and restores the original spec", async () => {
		renderEditor();
		const user = userEvent.setup();

		expect(screen.queryByRole("button", { name: "Reset" })).not.toBeInTheDocument();
		const name = screen.getByLabelText("Task name");
		await user.type(name, "-extra");
		const reset = await screen.findByRole("button", { name: "Reset" });
		await user.click(reset);
		expect(screen.getByLabelText("Task name")).toHaveValue("settings-migration");
	});

	it("renders the Cancel/close chrome only when onClose is provided", () => {
		const { rerender } = render(
			<QueryClientProvider client={new QueryClient()}>
				<TodoSpecEditor session={todo} />
			</QueryClientProvider>,
		);
		// Page variant (no onClose): no dismiss affordances.
		expect(screen.queryByRole("button", { name: "Close task detail" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument();

		rerender(
			<QueryClientProvider client={new QueryClient()}>
				<TodoSpecEditor session={todo} onClose={vi.fn()} />
			</QueryClientProvider>,
		);
		expect(screen.getByRole("button", { name: "Close task detail" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
	});
});
