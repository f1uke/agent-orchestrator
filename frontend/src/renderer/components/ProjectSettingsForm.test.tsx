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

// Every setting is a collapsed row (SettingRow): its control is not in the DOM
// until the row is opened. These tests are about the controls, not about the
// disclosure, so open every row in whichever section is currently showing.
async function openRows() {
	await screen.findAllByTestId("setting-row");
	for (const row of screen.getAllByTestId("setting-row")) {
		if (row.getAttribute("aria-expanded") === "false") await userEvent.click(row);
	}
}

// The two-pane shell shows one section at a time; navigate to a section's nav
// button before interacting with its fields. The draft lives above the sections
// so edits survive navigation and one save bar commits the whole config.
// Section names follow the variant-B cut: sections are named for what a setting
// acts on rather than for the shape of the config file.
type ProjectSection = "Repository & branches" | "Starting a task" | "What agents are told" | "Incoming & outgoing";
async function goToSection(name: ProjectSection) {
	// findByRole waits for the shell (and its nav) to mount after the project loads.
	await userEvent.click(await screen.findByRole("button", { name: new RegExp(`^${name}`) }));
	await openRows();
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

// The global root-CA list a project's simulators inherit unless it overrides it.
const globalSimTrustResponse = {
	data: {
		caFiles: ["~/Library/Application Support/com.proxyman.NSProxy/app-data/proxyman-ca.pem"],
		defaultCaFiles: ["~/Library/Application Support/com.proxyman.NSProxy/app-data/proxyman-ca.pem"],
		found: [true],
	},
	error: undefined,
};

const claudeProfilesResponse = {
	data: {
		profiles: [
			{ name: "Subscription", settingsFile: "", builtin: true },
			{ name: "OmniRoute", settingsFile: "~/.claude/settings-omniroute.json", builtin: true },
		],
	},
	error: undefined,
};

function mockProject(project: Record<string, unknown>) {
	getMock.mockImplementation(async (path: string) => {
		if (path === "/api/v1/agents") return agentCatalogResponse;
		if (path === "/api/v1/settings/sim-trust") return globalSimTrustResponse;
		if (path === "/api/v1/settings/claude-profiles") return claudeProfilesResponse;
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

		// Repository & branches is the default section.
		expect(await screen.findByText("git@github.com:acme/project-one.git")).toBeInTheDocument();
		await openRows();
		expect(screen.getByLabelText("Default branch")).toHaveValue("develop");
		expect(screen.getByLabelText("Session prefix")).toHaveValue("po");

		await userEvent.clear(screen.getByLabelText("Default branch"));
		await userEvent.type(screen.getByLabelText("Default branch"), "release");
		await userEvent.clear(screen.getByLabelText("Session prefix"));
		await userEvent.type(screen.getByLabelText("Session prefix"), "rel");

		await goToSection("Starting a task");
		const workerAgent = screen.getByRole("combobox", { name: "Worker agent" });
		const orchestratorAgent = screen.getByRole("combobox", { name: "Orchestrator agent" });
		const permissionMode = screen.getByRole("combobox", { name: "Permission mode" });
		// Once the agent catalog resolves the combobox shows the catalog label.
		await waitFor(() => expect(workerAgent).toHaveTextContent("Codex"));
		expect(orchestratorAgent).toHaveTextContent("Claude Code");
		expect(permissionMode).toHaveTextContent("Auto");
		// The reviewer is about what leaves the project, so it sits with intake and
		// the approval rule rather than with the agents that start a task.
		await goToSection("Incoming & outgoing");
		expect(screen.getByRole("combobox", { name: "Default reviewer agent" })).toHaveTextContent("claude-code");
		await goToSection("Starting a task");

		// Navigating away unmounted this section, so the earlier element handles are
		// detached - re-query them before driving the selects.
		// OpenCode is open-ended (free-form model), so switching the worker to it
		// preserves the stored value rather than clearing it — only a fixed target
		// that can't run the value resets to default.
		await chooseOption(screen.getByRole("combobox", { name: "Worker agent" }), "OpenCode");
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Goose");
		await chooseOption(screen.getByRole("combobox", { name: "Permission mode" }), "Bypass permissions");

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
		await goToSection("Starting a task");

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

	describe("Claude profile", () => {
		const project = (config: Record<string, unknown>) => ({
			id: "proj-1",
			name: "P",
			kind: "single_repo",
			path: "/repo/p",
			repo: "git@github.com:acme/p.git",
			defaultBranch: "main",
			config: { env: { FOO: "bar" }, ...config },
		});

		it("reads an unset profile as Subscription and saves the one picked", async () => {
			mockProject(project({ worker: { agent: "claude-code" }, orchestrator: { agent: "codex" } }));
			renderSettings();
			await goToSection("Starting a task");

			const select = await screen.findByRole("combobox", { name: "Claude profile" });
			expect(select).toHaveTextContent("Subscription");
			await chooseOption(select, "OmniRoute");
			await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

			await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
			const body = putMock.mock.calls[0][1].body.config;
			expect(body.claudeProfile).toBe("OmniRoute");
			expect(body.env).toEqual({ FOO: "bar" });
		});

		it("saves Subscription back as an unset profile", async () => {
			mockProject(
				project({ worker: { agent: "codex" }, orchestrator: { agent: "claude-code" }, claudeProfile: "OmniRoute" }),
			);
			renderSettings();
			await goToSection("Starting a task");

			const select = await screen.findByRole("combobox", { name: "Claude profile" });
			expect(select).toHaveTextContent("OmniRoute");
			await chooseOption(select, "Subscription");
			await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

			await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
			expect(putMock.mock.calls[0][1].body.config.claudeProfile).toBeUndefined();
		});

		it("is hidden when neither agent is Claude Code", async () => {
			mockProject(project({ worker: { agent: "codex" }, orchestrator: { agent: "codex" } }));
			renderSettings();
			await goToSection("Starting a task");

			await screen.findByRole("combobox", { name: "Worker model" });
			expect(screen.queryByRole("button", { name: /^Claude profile/ })).not.toBeInTheDocument();
		});
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
		await goToSection("Starting a task");

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
		await goToSection("Starting a task");

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
		await goToSection("Starting a task");
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
		await goToSection("What agents are told");

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
		await goToSection("What agents are told");

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
		await goToSection("What agents are told");

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
		await goToSection("What agents are told");

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
		await goToSection("What agents are told");
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

	it("turns script-only driving on for an Android app and saves product, platform and store", async () => {
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
		await goToSection("What agents are told");

		// Off for a project that never configured it, and the two fields that only
		// mean something when it is on are not offered.
		const mode = await screen.findByRole("combobox", { name: "Drive devices only through scripts" });
		expect(mode).toHaveTextContent("Off");
		expect(screen.queryByLabelText("Scripts product")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Verify skill")).not.toBeInTheDocument();

		await chooseOption(mode, "Scripts only - Android");
		await openRows();
		await userEvent.type(screen.getByLabelText("Scripts product"), "nter");
		await userEvent.type(screen.getByLabelText("Scripts store"), "/opt/scripts");
		await userEvent.type(screen.getByLabelText("Verify skill"), "projects/nter/verify-ios");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.mobileScripts).toEqual({
			platform: "android",
			product: "nter",
			store: "/opt/scripts",
			verifySkill: "projects/nter/verify-ios",
		});
		expect(body.config.env).toEqual({ TOKEN: "secret" });
	});

	it("loads script-only driving and turns it off by omitting the setting", async () => {
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
				mobileScripts: { platform: "ios", product: "nter" },
			},
		});

		renderSettings();
		await goToSection("What agents are told");

		const mode = await screen.findByRole("combobox", { name: "Drive devices only through scripts" });
		expect(mode).toHaveTextContent("Scripts only - iOS");
		expect(screen.getByLabelText("Scripts product")).toHaveValue("nter");
		// An unset store shows where the default store is rather than a blank.
		expect(screen.getByLabelText("Scripts store")).toHaveAttribute(
			"placeholder",
			"~/Documents/Projects/mobile-ui-scripts",
		);

		await chooseOption(mode, "Off");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.mobileScripts).toBeUndefined();
	});

	it("turns Uses Testiny on and keeps the rest of the config", async () => {
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
		await goToSection("What agents are told");

		// A project that never turned it on reads Off.
		const toggle = await screen.findByRole("switch", { name: "This project keeps its manual test cases in Testiny" });
		expect(toggle).not.toBeChecked();
		const row = screen.getByRole("button", { name: /^Uses Testiny/ });
		expect(row).toHaveTextContent("Off");

		await userEvent.click(toggle);
		expect(row).toHaveTextContent("On");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.usesTestiny).toBe(true);
		expect(body.config.env).toEqual({ TOKEN: "secret" });
	});

	it("loads Uses Testiny and turns it off by omitting it", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: { worker: { agent: "codex" }, orchestrator: { agent: "claude-code" }, usesTestiny: true },
		});

		renderSettings();
		await goToSection("What agents are told");

		const toggle = await screen.findByRole("switch", { name: "This project keeps its manual test cases in Testiny" });
		expect(toggle).toBeChecked();

		await userEvent.click(toggle);
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.usesTestiny).toBeUndefined();
	});

	it("blocks save when script-only driving has no product", async () => {
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
		await goToSection("What agents are told");

		await chooseOption(
			await screen.findByRole("combobox", { name: "Drive devices only through scripts" }),
			"Scripts only - iOS",
		);
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		expect(
			await screen.findByText("Script-only driving requires the product's folder in the scripts store."),
		).toBeInTheDocument();
		expect(putMock).not.toHaveBeenCalled();
	});

	// Config the simulator root-CA override must never disturb when it is saved.
	const otherMobileConfig = {
		worker: { agent: "codex" },
		orchestrator: { agent: "claude-code" },
		env: { TOKEN: "secret" },
		hasIOSSimulator: true,
		mobileScripts: { platform: "ios", product: "nter" },
		simProfile: { keep: ["com.example.app"] },
	};
	function mockSimTrustProject(simTrust?: { caFiles: string[] }) {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: { ...otherMobileConfig, ...(simTrust ? { simTrust } : {}) },
		});
	}
	async function savedConfig() {
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const config = putMock.mock.calls[0]?.[1]?.body.config;
		expect(config.env).toEqual({ TOKEN: "secret" });
		expect(config.hasIOSSimulator).toBe(true);
		expect(config.mobileScripts).toEqual({ platform: "ios", product: "nter" });
		expect(config.simProfile).toEqual({ keep: ["com.example.app"] });
		return config;
	}

	it("inherits the global simulator root CAs, then names this project's own files", async () => {
		mockSimTrustProject();
		renderSettings();
		await goToSection("What agents are told");

		const mode = await screen.findByRole("combobox", { name: "Simulator root CAs" });
		expect(mode).toHaveTextContent("Use the global list");
		// The list being inherited is stated at the field.
		expect(await screen.findByText("proxyman-ca.pem")).toBeInTheDocument();
		expect(screen.queryByLabelText("Root-CA files for this project")).not.toBeInTheDocument();

		await chooseOption(mode, "Use these files");
		await userEvent.type(
			screen.getByLabelText("Root-CA files for this project"),
			" /certs/a.pem {Enter}{Enter}~/b.pem",
		);
		const config = await savedConfig();
		expect(config.simTrust).toEqual({ caFiles: ["/certs/a.pem", "~/b.pem"] });
	});

	it("loads this project's own simulator root CAs and switches to trusting nothing", async () => {
		mockSimTrustProject({ caFiles: ["/certs/a.pem", "~/b.pem"] });
		renderSettings();
		await goToSection("What agents are told");

		const mode = await screen.findByRole("combobox", { name: "Simulator root CAs" });
		expect(mode).toHaveTextContent("Use these files");
		expect(screen.getByLabelText("Root-CA files for this project")).toHaveValue("/certs/a.pem\n~/b.pem");

		await chooseOption(mode, "Trust nothing on this project");
		const config = await savedConfig();
		// An empty list, not an absent one: absent would inherit the global list.
		expect(config.simTrust).toEqual({ caFiles: [] });
	});

	it("loads a trust-nothing project and goes back to the global list by omitting the setting", async () => {
		mockSimTrustProject({ caFiles: [] });
		renderSettings();
		await goToSection("What agents are told");

		const mode = await screen.findByRole("combobox", { name: "Simulator root CAs" });
		expect(mode).toHaveTextContent("Trust nothing on this project");

		await userEvent.click(screen.getByRole("button", { name: "Use global default" }));
		expect(mode).toHaveTextContent("Use the global list");
		const config = await savedConfig();
		// Undefined is dropped from the JSON body, so the daemon stores no override.
		expect(config.simTrust).toBeUndefined();
	});

	it("blocks save when this project's own simulator root-CA list is empty", async () => {
		mockSimTrustProject();
		renderSettings();
		await goToSection("What agents are told");

		await chooseOption(await screen.findByRole("combobox", { name: "Simulator root CAs" }), "Use these files");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		expect(
			await screen.findByText("Name at least one root-CA file, or choose to trust nothing on this project."),
		).toBeInTheDocument();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("turns automatic crew formation off for the project, and back on", async () => {
		// The human chose "settable from both the app and the CLI" for one stated
		// reason: a flag you cannot see is a flag you forget. This surface is the
		// seeing half — the project's own settings say, in one place, whether AO
		// will ever form a crew here.
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: { worker: { agent: "codex" }, orchestrator: { agent: "claude-code" }, env: { TOKEN: "secret" } },
		});

		renderSettings();
		await goToSection("Starting a task");

		// Opt-in: a project that never configured it keeps forming crews.
		const toggle = await screen.findByLabelText("Never form a crew automatically");
		expect(toggle).not.toBeChecked();

		await userEvent.click(toggle);
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.disableAutoCrew).toBe(true);
		// It must not be implemented by demoting the task size: nothing else about
		// how this project spawns changes, and config the form does not expose
		// survives the round-trip.
		expect(body.config.env).toEqual({ TOKEN: "secret" });
	});

	it("loads an already-crew-off project and can turn automatic crew back on", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: { worker: { agent: "codex" }, orchestrator: { agent: "claude-code" }, disableAutoCrew: true },
		});

		renderSettings();
		await goToSection("Starting a task");
		const toggle = await screen.findByLabelText("Never form a crew automatically");
		expect(toggle).toBeChecked();

		await userEvent.click(toggle);
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		// Automatic crew IS the default, so "on" is the absence of the field.
		expect(body.config.disableAutoCrew).toBeUndefined();
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
		await goToSection("Incoming & outgoing");

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
		await goToSection("Repository & branches");

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
		await goToSection("Incoming & outgoing");
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
		await goToSection("Incoming & outgoing");
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
		await goToSection("Incoming & outgoing");
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
		await goToSection("Repository & branches");

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
		await goToSection("Starting a task");

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
		await goToSection("Starting a task");
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
		await goToSection("Incoming & outgoing");
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
		await goToSection("Incoming & outgoing");
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
		await goToSection("Incoming & outgoing");
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
		await goToSection("Repository & branches");

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
		await goToSection("Repository & branches");

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
		await goToSection("Repository & branches");

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
		await goToSection("Repository & branches");

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
		await goToSection("Repository & branches");
		await userEvent.type(screen.getByLabelText("Default branch"), "x");
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

		await goToSection("Starting a task");
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

	it("turns the check-in gate on and saves it, leaving config the form does not expose alone", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: { worker: { agent: "codex" }, orchestrator: { agent: "claude-code" }, env: { TOKEN: "secret" } },
		});

		renderSettings();
		await goToSection("Starting a task");

		// Opt-in: a project that never configured it runs straight from brief to code.
		const toggle = await screen.findByLabelText("Check in with me before implementing");
		expect(toggle).not.toBeChecked();

		await userEvent.click(toggle);
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.pauseBeforeImplementing).toBe(true);
		expect(body.config.env).toEqual({ TOKEN: "secret" });
	});

	it("loads an already-gated project and can turn the check-in gate back off", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: { worker: { agent: "codex" }, orchestrator: { agent: "claude-code" }, pauseBeforeImplementing: true },
		});

		renderSettings();
		await goToSection("Starting a task");
		const toggle = await screen.findByLabelText("Check in with me before implementing");
		expect(toggle).toBeChecked();

		await userEvent.click(toggle);
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		// Not pausing IS the default, so "off" is the absence of the field.
		expect(body.config.pauseBeforeImplementing).toBeUndefined();
	});

	it("turns learning from sessions on and saves it, leaving config the form does not expose alone", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: { worker: { agent: "codex" }, orchestrator: { agent: "claude-code" }, env: { TOKEN: "secret" } },
		});

		renderSettings();
		await goToSection("Incoming & outgoing");

		// Opt-in: a project that never configured it is never read.
		const toggle = await screen.findByLabelText("Learn from sessions");
		expect(toggle).not.toBeChecked();

		await userEvent.click(toggle);
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.learnFromSessions).toBe(true);
		expect(body.config.env).toEqual({ TOKEN: "secret" });
	});

	it("loads a learning project and can turn learning back off", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: { worker: { agent: "codex" }, orchestrator: { agent: "claude-code" }, learnFromSessions: true },
		});

		renderSettings();
		await goToSection("Incoming & outgoing");
		const toggle = await screen.findByLabelText("Learn from sessions");
		expect(toggle).toBeChecked();

		await userEvent.click(toggle);
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		// Learning is opt-in, so "off" is the absence of the field.
		expect(body.config.learnFromSessions).toBeUndefined();
	});
});
