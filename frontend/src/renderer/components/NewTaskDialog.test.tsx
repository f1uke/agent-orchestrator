import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NewTaskDialog } from "./NewTaskDialog";
import { registerTerminalFocus } from "../lib/terminal-focus";

const { getMock, postMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
}));

// The Jira field now embeds the live search picker; stub the search hook so these
// dialog tests stay focused on task creation (and never hit the network).
vi.mock("../hooks/useSessionJiraContext", () => ({
	useJiraSearch: () => ({ data: [], isFetching: false, isError: false, error: null }),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: (...args: unknown[]) => getMock(...args),
		POST: (...args: unknown[]) => postMock(...args),
	},
	apiErrorMessage: (error: unknown, fallback = "Request failed") => {
		if (typeof error === "object" && error !== null && "message" in error) {
			const body = error as { code?: unknown; message: unknown };
			const message = String(body.message);
			return typeof body.code === "string" && body.code !== "" ? `${message} (${body.code})` : message;
		}
		return fallback;
	},
}));

function renderDialog() {
	const onCreated = vi.fn();
	const onOpenChange = vi.fn();
	render(
		<QueryClientProvider client={new QueryClient()}>
			<NewTaskDialog open projectId="proj-1" onCreated={onCreated} onOpenChange={onOpenChange} />
		</QueryClientProvider>,
	);
	return { onCreated, onOpenChange };
}

function spawnBody() {
	return (postMock.mock.calls[0][1] as { body: Record<string, unknown> }).body;
}

async function waitForAgentCatalog() {
	await waitFor(() => expect(screen.getAllByText("Claude Code").length).toBeGreaterThan(0));
}

beforeEach(() => {
	getMock.mockReset().mockImplementation(async (path: string) => {
		if (path === "/api/v1/agents") {
			return {
				data: {
					supported: [
						{ id: "claude-code", label: "Claude Code" },
						{ id: "cursor", label: "Cursor" },
						{ id: "kiro", label: "Kiro" },
					],
					installed: [
						{ id: "claude-code", label: "Claude Code", authStatus: "authorized" },
						{ id: "cursor", label: "Cursor", authStatus: "authorized" },
						{ id: "kiro", label: "Kiro", authStatus: "unknown" },
					],
					authorized: [
						{ id: "claude-code", label: "Claude Code", authStatus: "authorized" },
						{ id: "cursor", label: "Cursor", authStatus: "authorized" },
					],
				},
				error: undefined,
			};
		}
		if (path === "/api/v1/projects/{id}/branches") {
			return { data: { branches: [] }, error: undefined };
		}
		return {
			data: { status: "ok", project: { id: "proj-1", config: { worker: { agent: "claude-code" } } } },
			error: undefined,
		};
	});
	postMock.mockReset().mockResolvedValue({ data: { session: { id: "task-1" } }, error: undefined });
});

afterEach(() => vi.restoreAllMocks());

describe("NewTaskDialog", () => {
	it("preselects the project's default agent and omits harness so the daemon applies it", async () => {
		const { onCreated, onOpenChange } = renderDialog();
		const user = userEvent.setup();

		await waitForAgentCatalog();

		await user.type(screen.getByLabelText("Title"), "Fix fallback renderer");
		await user.type(screen.getByLabelText("Brief"), "Restore the fallback renderer after WebGL init fails.");
		await user.click(screen.getByRole("button", { name: "Start now" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions", {
			body: {
				projectId: "proj-1",
				kind: "worker",
				harness: undefined,
				issueId: "Fix fallback renderer",
				prompt: "Restore the fallback renderer after WebGL init fails.",
				branch: undefined,
				autoNameBranch: true,
			},
		});
		expect(onCreated).toHaveBeenCalledWith("task-1");
		expect(onOpenChange).toHaveBeenCalledWith(false);
	}, 10_000);

	it("auto-names the branch when the new-branch-name field is left blank", async () => {
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.type(screen.getByLabelText("Title"), "T");
		await user.type(screen.getByLabelText("Brief"), "B");
		await user.click(screen.getByRole("button", { name: "Start now" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(spawnBody().branch).toBeUndefined();
		expect(spawnBody().autoNameBranch).toBe(true);
	});

	it("uses the typed branch name and skips auto-naming", async () => {
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.type(screen.getByLabelText("Title"), "T");
		await user.type(screen.getByLabelText("Brief"), "B");
		await user.type(screen.getByLabelText("New branch name"), "feature/foo");
		await user.click(screen.getByRole("button", { name: "Start now" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(spawnBody().branch).toBe("feature/foo");
		expect(spawnBody().autoNameBranch).toBeUndefined();
	});

	it("renders the Start from, New branch name, and Agent fields in that order", async () => {
		renderDialog();
		await waitForAgentCatalog();

		const startFrom = screen.getByLabelText("Start from");
		const newBranchName = screen.getByLabelText("New branch name");
		const agentField = screen.getByRole("combobox", { name: "Agent" });

		expect(startFrom.compareDocumentPosition(newBranchName) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(newBranchName.compareDocumentPosition(agentField) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
	});

	it("initializes Start from to the project default branch and includes baseBranch in the payload", async () => {
		getMock.mockReset().mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents") {
				return {
					data: {
						supported: [{ id: "claude-code", label: "Claude Code" }],
						installed: [{ id: "claude-code", label: "Claude Code", authStatus: "authorized" }],
						authorized: [{ id: "claude-code", label: "Claude Code", authStatus: "authorized" }],
					},
					error: undefined,
				};
			}
			if (path === "/api/v1/projects/{id}/branches") {
				return { data: { branches: ["main", "develop", "origin/STAR-2270"] }, error: undefined };
			}
			return {
				data: {
					status: "ok",
					project: { id: "proj-1", defaultBranch: "main", config: { worker: { agent: "claude-code" } } },
				},
				error: undefined,
			};
		});
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		const startFrom = screen.getByLabelText("Start from");
		await waitFor(() => expect(startFrom).toHaveValue("main"));

		await user.click(startFrom);
		await user.type(startFrom, "develop");
		await user.click(await screen.findByText("develop"));

		await user.type(screen.getByLabelText("Title"), "T");
		await user.type(screen.getByLabelText("Brief"), "B");
		await user.click(screen.getByRole("button", { name: "Start now" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(spawnBody().baseBranch).toBe("develop");
	});

	it("sends the chosen harness when the user overrides the default", async () => {
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.type(screen.getByLabelText("Title"), "T");
		await user.type(screen.getByLabelText("Brief"), "B");

		await user.click(screen.getByRole("combobox", { name: "Agent" }));
		await user.click(await screen.findByRole("option", { name: "Cursor" }));

		await user.click(screen.getByRole("button", { name: "Start now" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(spawnBody().harness).toBe("cursor");
	});

	it("allows selecting an installed agent with unknown auth", async () => {
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.click(screen.getByRole("combobox", { name: "Agent" }));
		const options = await screen.findAllByRole("option");
		expect(options.map((option) => option.textContent)).toEqual(["Claude Code", "Cursor", "KiroAuth unknown"]);
		expect(options[2]).not.toHaveAttribute("aria-disabled", "true");
		await user.click(options[2]);

		await user.type(screen.getByLabelText("Title"), "T");
		await user.type(screen.getByLabelText("Brief"), "B");
		await user.click(screen.getByRole("button", { name: "Start now" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(spawnBody().harness).toBe("kiro");
	});

	it("returns focus to the orchestrator terminal when closed without starting a task", async () => {
		const focus = vi.fn();
		const unregister = registerTerminalFocus(focus);
		const user = userEvent.setup();

		function Harness() {
			const [open, setOpen] = useState(true);
			return (
				<QueryClientProvider client={new QueryClient()}>
					<NewTaskDialog open={open} projectId="proj-1" onCreated={vi.fn()} onOpenChange={setOpen} />
				</QueryClientProvider>
			);
		}
		render(<Harness />);
		await waitForAgentCatalog();

		await user.click(screen.getByRole("button", { name: "Close new task dialog" }));

		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(focus).toHaveBeenCalled();
		unregister();
	});

	it("omits startImmediately in the default Start-now mode", async () => {
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.type(screen.getByLabelText("Title"), "Now task");
		await user.type(screen.getByLabelText("Brief"), "Start it now.");
		await user.click(screen.getByRole("button", { name: "Start now" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(spawnBody().startImmediately).toBeUndefined();
	});

	it("queues a deferred TODO with startImmediately=false when Add to TODO is selected", async () => {
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.type(screen.getByLabelText("Title"), "Deferred task");
		await user.type(screen.getByLabelText("Brief"), "Do it later.");
		await user.click(screen.getByRole("button", { name: "Mode: queue in TODO" }));
		await user.click(screen.getByRole("button", { name: "Add to TODO" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(spawnBody().startImmediately).toBe(false);
	});

	it("disables the primary action until both title and brief are filled", async () => {
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		const submit = screen.getByRole("button", { name: "Start now" });
		expect(submit).toBeDisabled();

		await user.type(screen.getByLabelText("Title"), "T");
		expect(submit).toBeDisabled();

		await user.type(screen.getByLabelText("Brief"), "B");
		expect(submit).toBeEnabled();
	});

	it.each([
		{
			code: "AGENT_BINARY_NOT_FOUND",
			message: "agent binary not found on PATH",
		},
		{
			code: "RUNTIME_PREREQUISITE_MISSING",
			message: "tmux required on macOS/Linux but not in PATH",
		},
		{
			code: "INTERNAL",
			message: "runtime launch failed",
		},
	])("displays daemon spawn errors for $code", async ({ code, message }) => {
		postMock.mockResolvedValueOnce({
			data: undefined,
			error: { code, message },
		});
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.type(screen.getByLabelText("Title"), "Fix fallback renderer");
		await user.type(screen.getByLabelText("Brief"), "Restore fallback renderer.");
		await user.click(screen.getByRole("button", { name: "Start now" }));

		expect(await screen.findByText(`${message} (${code})`)).toBeInTheDocument();
	});

	it("binds the session to jira:<KEY> and keeps the title as displayName when a Jira key is linked", async () => {
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.type(screen.getByLabelText(/Jira issue/i), "demo-101");
		await user.type(screen.getByLabelText("Title"), "Example story");
		await user.type(screen.getByLabelText("Brief"), "Build the sample UI.");
		await user.click(screen.getByRole("button", { name: "Start now" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		const body = spawnBody();
		expect(body.issueId).toBe("jira:DEMO-101");
		expect(body.displayName).toBe("Example story");
		expect(body.prompt).toBe("Build the sample UI.");
	});

	it("caps displayName at 20 characters for a Jira-linked task", async () => {
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.type(screen.getByLabelText(/Jira issue/i), "DEMO-101");
		await user.type(screen.getByLabelText("Title"), "A very long human title beyond twenty");
		await user.type(screen.getByLabelText("Brief"), "B");
		await user.click(screen.getByRole("button", { name: "Start now" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect((spawnBody().displayName as string).length).toBe(20);
	});

	it("keeps the title in issueId (no displayName) when no Jira key is linked", async () => {
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.type(screen.getByLabelText("Title"), "Plain task");
		await user.type(screen.getByLabelText("Brief"), "No Jira here.");
		await user.click(screen.getByRole("button", { name: "Start now" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		const body = spawnBody();
		expect(body.issueId).toBe("Plain task");
		expect(body.displayName).toBeUndefined();
	});

	it("rejects an invalid Jira key without submitting", async () => {
		renderDialog();
		const user = userEvent.setup();
		await waitForAgentCatalog();

		await user.type(screen.getByLabelText(/Jira issue/i), "not-a-key");
		await user.type(screen.getByLabelText("Title"), "T");
		await user.type(screen.getByLabelText("Brief"), "B");
		await user.click(screen.getByRole("button", { name: "Start now" }));

		expect(await screen.findByText(/Pick a Jira issue from the list/i)).toBeInTheDocument();
		expect(postMock).not.toHaveBeenCalled();
	});
});
