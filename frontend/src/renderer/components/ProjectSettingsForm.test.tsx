import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, putMock, postMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	putMock: vi.fn(),
	postMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: getMock,
		PUT: putMock,
		POST: postMock,
	},
	apiErrorMessage: (error: unknown) => {
		if (error instanceof Error) return error.message;
		if (typeof error === "object" && error !== null && "message" in error) {
			return String((error as { message: unknown }).message);
		}
		return "Request failed";
	},
}));

// The unified shell's scope switcher calls useNavigate, which needs a router
// context these unit renders don't provide. Preserve every other export and stub
// navigation to a no-op.
vi.mock("@tanstack/react-router", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-router")>();
	return { ...actual, useNavigate: () => vi.fn() };
});

import { ProjectSettingsForm } from "./ProjectSettingsForm";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import type { WorkspaceSummary } from "../types/workspace";

function renderSettings(projectId = "proj-1", workspaces?: WorkspaceSummary[]) {
	const queryClient = new QueryClient({
		defaultOptions: {
			queries: { retry: false },
			mutations: { retry: false },
		},
	});
	if (workspaces) {
		queryClient.setQueryData(workspaceQueryKey, workspaces);
	}
	render(
		<QueryClientProvider client={queryClient}>
			<ProjectSettingsForm projectId={projectId} />
		</QueryClientProvider>,
	);
	return queryClient;
}

// The two-pane shell shows one section at a time; navigate to a section's nav
// button before interacting with its fields. The draft lives above the sections
// so edits survive navigation and one save bar commits the whole config.
async function goToSection(name: "General" | "Agents" | "Prompts" | "Automation") {
	// findByRole waits for the shell (and its nav) to mount after the project loads.
	await userEvent.click(await screen.findByRole("button", { name }));
}

async function chooseOption(trigger: HTMLElement, optionName: string) {
	await userEvent.click(trigger);
	await userEvent.click(await screen.findByRole("option", { name: optionName }));
}

const agentCatalogResponse = {
	data: {
		supported: [
			{
				id: "claude-code",
				label: "Claude Code",
				models: [
					{ id: "opus", label: "Opus" },
					{ id: "sonnet", label: "Sonnet" },
					{ id: "haiku", label: "Haiku" },
					{ id: "claude-fable-5", label: "Fable" },
				],
			},
			{
				id: "codex",
				label: "Codex",
				models: [
					{ id: "gpt-5.6-sol", label: "GPT-5.6 Sol" },
					{ id: "gpt-5.6-terra", label: "GPT-5.6 Terra" },
				],
			},
			{ id: "goose", label: "Goose" },
			{ id: "kiro", label: "Kiro" },
			{
				id: "opencode",
				label: "OpenCode",
				modelsOpenEnded: true,
				models: [{ id: "anthropic/claude-opus-4-8", label: "Claude Opus 4.8" }],
			},
		],
		installed: [
			{ id: "claude-code", label: "Claude Code", authStatus: "authorized" },
			{ id: "codex", label: "Codex", authStatus: "authorized" },
			{ id: "goose", label: "Goose", authStatus: "authorized" },
			{ id: "kiro", label: "Kiro", authStatus: "unknown" },
			{ id: "opencode", label: "OpenCode", authStatus: "authorized" },
		],
		authorized: [
			{ id: "claude-code", label: "Claude Code", authStatus: "authorized" },
			{ id: "codex", label: "Codex", authStatus: "authorized" },
			{ id: "goose", label: "Goose", authStatus: "authorized" },
			{ id: "opencode", label: "OpenCode", authStatus: "authorized" },
		],
	},
	error: undefined,
};

function mockProject(project: Record<string, unknown>) {
	getMock.mockImplementation(async (path: string) => {
		if (path === "/api/v1/agents") return agentCatalogResponse;
		return {
			data: {
				status: "ok",
				project,
			},
			error: undefined,
		};
	});
}

beforeEach(() => {
	getMock.mockReset();
	putMock.mockReset();
	postMock.mockReset();
	putMock.mockResolvedValue({ data: { project: {} }, error: undefined });
	postMock.mockResolvedValue({
		data: { orchestrator: { id: "proj-1-orch-2" } },
		error: undefined,
		response: { status: 200 },
	});
});

describe("ProjectSettingsForm", () => {
	it("loads the current project settings and saves the exposed fields without dropping hidden config", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				defaultBranch: "develop",
				sessionPrefix: "po",
				env: { FOO: "bar" },
				symlinks: [".env"],
				postCreate: ["npm install"],
				worker: {
					agent: "codex",
					agentConfig: { model: "worker-model" },
				},
				orchestrator: { agent: "claude-code" },
				agentConfig: {
					model: "claude-opus-4-5",
					permissions: "auto",
				},
				reviewers: [{ harness: "claude-code" }],
			},
		});

		renderSettings();

		// General is the default section.
		expect(await screen.findByText("git@github.com:acme/project-one.git")).toBeInTheDocument();
		expect(screen.getByLabelText("Default branch")).toHaveValue("develop");
		expect(screen.getByLabelText("Session prefix")).toHaveValue("po");

		await userEvent.clear(screen.getByLabelText("Default branch"));
		await userEvent.type(screen.getByLabelText("Default branch"), "release");
		await userEvent.clear(screen.getByLabelText("Session prefix"));
		await userEvent.type(screen.getByLabelText("Session prefix"), "rel");

		await goToSection("Agents");
		const workerAgent = screen.getByRole("combobox", { name: "Worker agent" });
		const orchestratorAgent = screen.getByRole("combobox", { name: "Orchestrator agent" });
		const permissionMode = screen.getByRole("combobox", { name: "Permission mode" });
		const reviewerAgent = screen.getByRole("combobox", { name: "Default reviewer agent" });
		// Once the agent catalog resolves the combobox shows the catalog label.
		await waitFor(() => expect(workerAgent).toHaveTextContent("Codex"));
		expect(orchestratorAgent).toHaveTextContent("Claude Code");
		expect(permissionMode).toHaveTextContent("Auto");
		expect(reviewerAgent).toHaveTextContent("claude-code");

		// OpenCode is open-ended (free-form model), so switching the worker to it
		// preserves the stored value rather than clearing it — only a fixed target
		// that can't run the value resets to default.
		await chooseOption(workerAgent, "OpenCode");
		await chooseOption(orchestratorAgent, "Goose");
		await chooseOption(permissionMode, "Bypass permissions");

		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}/config", {
			params: { path: { id: "proj-1" } },
			body: {
				config: {
					defaultBranch: "release",
					sessionPrefix: "rel",
					env: { FOO: "bar" },
					symlinks: [".env"],
					postCreate: ["npm install"],
					worker: {
						agent: "opencode",
						agentConfig: { model: "worker-model" },
					},
					orchestrator: {
						agent: "goose",
						agentConfig: undefined,
					},
					agentConfig: {
						model: "claude-opus-4-5",
						permissions: "bypass-permissions",
					},
					reviewers: [{ harness: "claude-code" }],
				},
			},
		});
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock).toHaveBeenCalledWith("/api/v1/orchestrators", {
			body: { projectId: "proj-1", clean: true },
		});
		expect(await screen.findByText("Saved.")).toBeInTheDocument();
	}, 20_000);

	it("selects separate orchestrator and worker models and saves them per kind", async () => {
		mockProject({
			id: "proj-1",
			name: "P",
			kind: "single_repo",
			path: "/repo/p",
			repo: "git@github.com:acme/p.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "claude-code" },
				orchestrator: { agent: "claude-code" },
				env: { FOO: "bar" },
			},
		});
		renderSettings();
		await goToSection("Agents");

		const orchestratorModel = await screen.findByRole("combobox", { name: "Orchestrator model" });
		const workerModel = screen.getByRole("combobox", { name: "Worker model" });
		// Unset renders as the agent-default option, labelled with the agent.
		expect(orchestratorModel).toHaveTextContent("Default (Claude Code default)");
		expect(workerModel).toHaveTextContent("Default (Claude Code default)");

		await chooseOption(orchestratorModel, "Sonnet");
		await chooseOption(workerModel, "Opus");

		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0][1].body.config;
		expect(body.orchestrator).toEqual({ agent: "claude-code", agentConfig: { model: "sonnet" } });
		expect(body.worker).toEqual({ agent: "claude-code", agentConfig: { model: "opus" } });
		expect(body.env).toEqual({ FOO: "bar" }); // hidden config preserved
	});

	it("round-trips a free-typed custom model for an open-ended worker agent", async () => {
		mockProject({
			id: "proj-1",
			name: "P",
			kind: "single_repo",
			path: "/repo/p",
			repo: "git@github.com:acme/p.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "opencode" },
				orchestrator: { agent: "claude-code" },
				env: { FOO: "bar" },
			},
		});
		renderSettings();
		await goToSection("Agents");

		// An open-ended agent's model is an editable input, not a fixed Select.
		const workerModel = await screen.findByRole("combobox", { name: "Worker model" });
		expect(workerModel.tagName).toBe("INPUT");
		expect(workerModel).toHaveAttribute("placeholder", "anthropic/claude-opus-4-8");

		// A custom id that is not one of the catalog suggestions must round-trip.
		await userEvent.type(workerModel, "openrouter/anthropic/claude-3.7");

		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0][1].body.config;
		expect(body.worker).toEqual({ agent: "opencode", agentConfig: { model: "openrouter/anthropic/claude-3.7" } });
		expect(body.env).toEqual({ FOO: "bar" }); // hidden config preserved
	});

	it("clears a free-form model when the worker switches to a fixed agent that can't run it", async () => {
		mockProject({
			id: "proj-1",
			name: "P",
			kind: "single_repo",
			path: "/repo/p",
			repo: "git@github.com:acme/p.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "opencode", agentConfig: { model: "openrouter/anthropic/claude-3.7" } },
				orchestrator: { agent: "claude-code" },
			},
		});
		renderSettings();
		await goToSection("Agents");

		// Switching to claude-code (fixed tiers) can't run the free-form value, so
		// the model resets to that agent's default rather than carrying it over.
		await chooseOption(screen.getByRole("combobox", { name: "Worker agent" }), "Claude Code");

		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0][1].body.config;
		expect(body.worker).toEqual({ agent: "claude-code", agentConfig: undefined });
	});

	it("shows a hint instead of a model selector for an agent with no selectable tiers", async () => {
		mockProject({
			id: "proj-1",
			name: "P",
			kind: "single_repo",
			path: "/repo/p",
			repo: "git@github.com:acme/p.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "goose" },
				orchestrator: { agent: "claude-code" },
			},
		});
		renderSettings();
		await goToSection("Agents");
		// The claude-code orchestrator offers tiers...
		expect(await screen.findByRole("combobox", { name: "Orchestrator model" })).toBeInTheDocument();
		// ...but Goose exposes none, so the worker model is a hint, not a selector.
		expect(screen.queryByRole("combobox", { name: "Worker model" })).not.toBeInTheDocument();
		expect(screen.getByText(/Goose uses its own default model/)).toBeInTheDocument();
	});

	it("edits per-kind additional prompts in the drawer and saves them without dropping hidden config", async () => {
		mockProject({
			id: "proj-1",
			name: "P",
			kind: "single_repo",
			path: "/repo/p",
			repo: "git@github.com:acme/p.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
				env: { FOO: "bar" },
				systemPromptAdditions: { worker: "existing worker note" },
			},
		});
		renderSettings();
		await goToSection("Prompts");

		// The overridden Worker row reads Customized; open its drawer to edit.
		await userEvent.click(await screen.findByRole("button", { name: "Edit Worker additional prompt" }));
		const drawer = await screen.findByRole("dialog");
		const worker = within(drawer).getByRole("textbox") as HTMLTextAreaElement;
		await waitFor(() => expect(worker.value).toBe("existing worker note"));
		await userEvent.clear(worker);
		await userEvent.type(worker, "new worker note");
		await userEvent.click(screen.getByRole("button", { name: "Done" }));

		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0][1].body.config;
		expect(body.systemPromptAdditions).toEqual({
			orchestrator: undefined,
			worker: "new worker note",
			reviewer: undefined,
		});
		expect(body.env).toEqual({ FOO: "bar" }); // hidden config preserved
	});

	it("loads and saves the per-project response-language override without dropping hidden config", async () => {
		mockProject({
			id: "proj-1",
			name: "P",
			kind: "single_repo",
			path: "/repo/p",
			repo: "git@github.com:acme/p.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
				env: { FOO: "bar" },
				responseLanguage: "Thai",
			},
		});
		renderSettings();
		await goToSection("Prompts");

		// The stored override shows on the select; changing it dirties the bar.
		const language = await screen.findByRole("combobox", { name: "Response language" });
		expect(language).toHaveTextContent("Thai");
		await chooseOption(language, "Japanese");

		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0][1].body.config;
		expect(body.responseLanguage).toBe("Japanese");
		expect(body.env).toEqual({ FOO: "bar" }); // hidden config preserved
	});

	it("omits responseLanguage when the project inherits the global default", async () => {
		mockProject({
			id: "proj-1",
			name: "P",
			kind: "single_repo",
			path: "/repo/p",
			repo: "git@github.com:acme/p.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
				responseLanguage: "Thai",
			},
		});
		renderSettings();
		await goToSection("Prompts");

		const language = await screen.findByRole("combobox", { name: "Response language" });
		await chooseOption(language, "Inherit global default");

		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0][1].body.config;
		expect(body.responseLanguage).toBeUndefined();
	});

	// One flag behind three effects (Browser tab, `ao preview` guidance, the
	// `ao preview` command), so they can never be set to contradict each other.
	it("turns the project's web UI on and saves it without dropping hidden config", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
				env: { TOKEN: "secret" },
			},
		});

		renderSettings();
		await screen.findByText("git@github.com:acme/project-one.git");

		// Opt-in: off for a project that never configured it.
		const toggle = await screen.findByLabelText("This project has a web UI");
		expect(toggle).not.toBeChecked();

		await userEvent.click(toggle);
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.hasWebUI).toBe(true);
		// Config the form does not expose must survive the round-trip.
		expect(body.config.env).toEqual({ TOKEN: "secret" });
	});

	it("loads an already-enabled web UI and can turn it back off", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
				hasWebUI: true,
			},
		});

		renderSettings();
		const toggle = await screen.findByLabelText("This project has a web UI");
		expect(toggle).toBeChecked();

		await userEvent.click(toggle);
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		// Off is the default, so it is omitted rather than written as false — an
		// otherwise-unset config still persists as unset.
		expect(body.config.hasWebUI).toBeUndefined();
	});

	it("shows the approval-rule toggle for GitLab projects only, and saves the enabled rule with a threshold", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@gitlab.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();
		await screen.findByText("git@gitlab.com:acme/project-one.git");
		await goToSection("Automation");

		// Off by default: the toggle is present and unchecked, the threshold hidden.
		const toggle = await screen.findByLabelText("Require approvals before Ready to merge");
		expect(toggle).not.toBeChecked();
		expect(screen.queryByLabelText("Required approvals")).not.toBeInTheDocument();

		// Enabling reveals the threshold input and dirties the save bar.
		await userEvent.click(toggle);
		const threshold = await screen.findByLabelText("Required approvals");
		expect(threshold).toHaveValue(null);

		await userEvent.type(threshold, "4");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.approvalRule).toEqual({ enabled: true, threshold: 4 });
	});

	it("omits the approval rule when the toggle is left off", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@gitlab.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();

		// A benign edit reveals the save bar; the approval rule stays off/omitted.
		await userEvent.type(await screen.findByLabelText("Session prefix"), "x");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.approvalRule).toBeUndefined();
	});

	it("hides the approval-rule card for a GitHub project", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();
		await screen.findByText("git@github.com:acme/project-one.git");
		await goToSection("Automation");
		expect(screen.queryByLabelText("Require approvals before Ready to merge")).not.toBeInTheDocument();
	});

	it("explains the missing approval rule when no git remote was detected", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "develop",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();
		await goToSection("Automation");
		// The card still cannot be offered (provider unknown), but its absence is
		// no longer silent — that silence is what made a GitLab project look like
		// it simply had no approval-rule setting.
		expect(screen.queryByLabelText("Require approvals before Ready to merge")).not.toBeInTheDocument();
		expect(await screen.findByText(/couldn't detect a git remote/i)).toBeInTheDocument();
	});

	it("stays quiet about the approval rule for a detected non-GitLab project", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();
		await goToSection("Automation");
		expect(screen.queryByText(/couldn't detect a git remote/i)).not.toBeInTheDocument();
	});

	it("shows the daemon validation message when save fails", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});
		putMock.mockResolvedValue({
			data: undefined,
			error: { message: "invalid permissions" },
		});

		renderSettings();

		await userEvent.type(await screen.findByLabelText("Default branch"), "x");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		expect(await screen.findByText("invalid permissions")).toBeInTheDocument();
		expect(screen.queryByText("Saved.")).not.toBeInTheDocument();
		expect(postMock).not.toHaveBeenCalled();
	});

	it("requires worker and orchestrator agents for existing projects missing role config", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {},
		});

		renderSettings();
		await goToSection("Agents");

		expect(await screen.findByText("Worker and orchestrator agents are required.")).toBeInTheDocument();
		expect(screen.getByRole("combobox", { name: "Worker agent" })).toHaveTextContent("Select worker agent");
		expect(screen.getByRole("combobox", { name: "Orchestrator agent" })).toHaveTextContent("Select orchestrator agent");

		// Pick only the worker agent → the bar appears but the guard still blocks
		// save because the orchestrator agent is still empty.
		await chooseOption(screen.getByRole("combobox", { name: "Worker agent" }), "Codex");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		expect(await screen.findAllByText("Worker and orchestrator agents are required.")).toHaveLength(2);
		expect(putMock).not.toHaveBeenCalled();
	});

	it("shows unknown-auth agents as selectable with a warning in project settings", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();
		await goToSection("Agents");
		const workerAgent = screen.getByRole("combobox", { name: "Worker agent" });
		await userEvent.click(workerAgent);
		const options = await screen.findAllByRole("option");
		expect(options.map((option) => option.textContent)).toEqual([
			"Claude Code",
			"Codex",
			"Goose",
			"OpenCode",
			"KiroAuth unknown",
		]);
		expect(options[4]).not.toHaveAttribute("aria-disabled", "true");
	});

	it("saves GitHub tracker intake settings, deriving the repo from the project's git origin", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();
		await goToSection("Automation");
		await userEvent.click(await screen.findByLabelText("Enable issue intake"));

		// Repository is display-only, derived from the project's own git origin — no
		// input to fill. Assignee is the only eligibility rule in v1.
		expect(screen.getByRole("link", { name: "acme/project-one" })).toHaveAttribute(
			"href",
			"https://github.com/acme/project-one",
		);
		await userEvent.type(screen.getByLabelText("Assignee"), "octocat");

		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.trackerIntake).toEqual({
			enabled: true,
			provider: "github",
			assignee: "octocat",
		});
	});

	it("saves GitLab tracker intake, deriving the nested repo path and a self-hosted link", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@gitlab.example.com:group/sub/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();
		await goToSection("Automation");
		await userEvent.click(await screen.findByLabelText("Enable issue intake"));

		// Nested GitLab group path is preserved (not truncated to two segments) and
		// the preview links to the self-hosted host, not github.com.
		expect(screen.getByRole("link", { name: "group/sub/project-one" })).toHaveAttribute(
			"href",
			"https://gitlab.example.com/group/sub/project-one",
		);
		await userEvent.type(screen.getByLabelText("Assignee"), "octocat");

		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.trackerIntake).toEqual({
			enabled: true,
			provider: "gitlab",
			assignee: "octocat",
		});
	});

	it("blocks save when intake is enabled with no assignee", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();
		await goToSection("Automation");
		await userEvent.click(await screen.findByLabelText("Enable issue intake"));
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		expect(await screen.findAllByText("Enabling intake requires an assignee.")).toHaveLength(2);
		expect(putMock).not.toHaveBeenCalled();
	});

	it("loads an existing git convention and saves the edited workflow and prefix", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
				gitConvention: { workflow: "gitflow" },
			},
		});

		renderSettings();

		const workflow = await screen.findByRole("combobox", { name: "Branch workflow" });
		expect(workflow).toHaveTextContent("gitflow");
		// gitflow does not require a prefix, but the input is available.
		expect(screen.getByLabelText("Branch prefix")).toHaveValue("");

		await chooseOption(workflow, "custom");
		await userEvent.type(screen.getByLabelText("Branch prefix"), "feat/");

		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.gitConvention).toEqual({ workflow: "custom", branchPrefix: "feat/" });
	});

	it("hides the branch-prefix input until a workflow is chosen and omits the convention when none", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();

		// None selected by default → no prefix input.
		await screen.findByLabelText("Branch workflow");
		expect(screen.queryByLabelText("Branch prefix")).not.toBeInTheDocument();

		await userEvent.type(screen.getByLabelText("Session prefix"), "x");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.gitConvention).toBeUndefined();
	});

	it("blocks save when a custom workflow has no branch prefix", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();

		const workflow = await screen.findByRole("combobox", { name: "Branch workflow" });
		await chooseOption(workflow, "custom");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		expect(await screen.findByText("A custom git workflow requires a branch prefix.")).toBeInTheDocument();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("Discard reverts every edited field and hides the Save button", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				defaultBranch: "develop",
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();

		const branch = await screen.findByLabelText("Default branch");
		await userEvent.clear(branch);
		await userEvent.type(branch, "release");
		expect(screen.getByRole("button", { name: "Save changes" })).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Discard" }));

		expect(screen.getByLabelText("Default branch")).toHaveValue("develop");
		expect(screen.queryByRole("button", { name: "Save changes" })).not.toBeInTheDocument();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("restarts when the saved orchestrator agent already differs from the running orchestrator", async () => {
		getMock.mockResolvedValue({
			data: {
				status: "ok",
				project: {
					id: "proj-1",
					name: "Project One",
					kind: "single_repo",
					path: "/repo/project-one",
					repo: "",
					defaultBranch: "main",
					config: {
						worker: { agent: "codex" },
						orchestrator: { agent: "goose" },
					},
				},
			},
			error: undefined,
		});

		renderSettings("proj-1", [
			{
				id: "proj-1",
				name: "Project One",
				path: "/repo/project-one",
				orchestratorAgent: "goose",
				sessions: [
					{
						id: "proj-1-orchestrator",
						workspaceId: "proj-1",
						workspaceName: "Project One",
						title: "Orchestrator",
						provider: "claude-code",
						kind: "orchestrator",
						branch: "ao/proj-1-orchestrator",
						status: "working",
						createdAt: "2026-07-03T00:00:00Z",
						updatedAt: "2026-07-03T00:00:00Z",
						prs: [],
					},
				],
			},
		]);

		// A benign edit reveals the save bar; saving restarts because the running
		// orchestrator's provider differs from the saved orchestrator agent.
		await userEvent.type(await screen.findByLabelText("Default branch"), "x");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock).toHaveBeenCalledWith("/api/v1/orchestrators", {
			body: { projectId: "proj-1", clean: true },
		});
	});

	it("keeps the config save successful when orchestrator replacement fails", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});
		postMock.mockResolvedValue({
			data: undefined,
			error: { message: "missing goose binary" },
			response: { status: 500 },
		});

		const queryClient = renderSettings();
		const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");

		await goToSection("Agents");
		const orchestratorAgent = await screen.findByRole("combobox", { name: "Orchestrator agent" });
		await chooseOption(orchestratorAgent, "Goose");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(await screen.findByText("Saved.")).toBeInTheDocument();
		expect(await screen.findByText("Orchestrator restart failed: missing goose binary")).toBeInTheDocument();
		expect(screen.queryByText("Save failed")).not.toBeInTheDocument();
		expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["project", "proj-1"] });
		expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: workspaceQueryKey });
	});
});
