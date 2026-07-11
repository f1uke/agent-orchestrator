import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TodoDetailDialog } from "./TodoDetailDialog";
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

function renderDetail(session: WorkspaceSession | null = todo) {
	const onOpenChange = vi.fn();
	const onStarted = vi.fn();
	render(
		<QueryClientProvider client={new QueryClient()}>
			<TodoDetailDialog session={session} onOpenChange={onOpenChange} onStarted={onStarted} />
		</QueryClientProvider>,
	);
	return { onOpenChange, onStarted };
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

describe("TodoDetailDialog", () => {
	it("renders the TODO spec (id, name, prompt, branches)", () => {
		renderDetail();
		expect(screen.getByText("proj-1-72")).toBeInTheDocument();
		expect(screen.getByLabelText("Task name")).toHaveValue("settings-migration");
		expect(screen.getByText(/TODO · not started/)).toBeInTheDocument();
		expect(screen.getByText("Migrate the autonudge setting into its own store.")).toBeInTheDocument();
	});

	it("starts the task via POST /start and reports the started id", async () => {
		const { onStarted } = renderDetail();
		const user = userEvent.setup();

		await user.click(screen.getByRole("button", { name: /Start work/ }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/start", {
			params: { path: { sessionId: "proj-1-72" } },
		});
		await waitFor(() => expect(onStarted).toHaveBeenCalledWith("proj-1-72"));
	});

	it("deletes the task after an inline confirm", async () => {
		renderDetail();
		const user = userEvent.setup();

		await user.click(screen.getByRole("button", { name: /^Delete$/ }));
		await user.click(screen.getByRole("button", { name: "Delete task" }));

		await waitFor(() => expect(deleteMock).toHaveBeenCalledTimes(1));
		expect(deleteMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}", {
			params: { path: { sessionId: "proj-1-72" }, query: { force: true } },
		});
	});

	it("renders nothing when there is no session", () => {
		const { container } = render(
			<QueryClientProvider client={new QueryClient()}>
				<TodoDetailDialog session={null} onOpenChange={vi.fn()} onStarted={vi.fn()} />
			</QueryClientProvider>,
		);
		expect(container).toBeEmptyDOMElement();
	});
});
