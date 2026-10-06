import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const {
	getMock,
	postMock,
	putMock,
	deleteMock,
	getMigration,
	setMigration,
	getUpdate,
	setUpdate,
	updGetStatus,
	updCheck,
	updDownload,
	updInstall,
	updOnStatus,
	getVersion,
} = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	putMock: vi.fn(),
	deleteMock: vi.fn(),
	getMigration: vi.fn(),
	setMigration: vi.fn(),
	getUpdate: vi.fn(),
	setUpdate: vi.fn(),
	updGetStatus: vi.fn(),
	updCheck: vi.fn(),
	updDownload: vi.fn(),
	updInstall: vi.fn(),
	updOnStatus: vi.fn(),
	getVersion: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock, PUT: putMock, DELETE: deleteMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") =>
		e instanceof Error ? e.message : ((e as { message?: string })?.message ?? fb),
}));
vi.mock("../lib/bridge", () => ({
	aoBridge: {
		app: { getVersion },
		appState: { getMigration, setMigration },
		updateSettings: { get: getUpdate, set: setUpdate },
		notifications: { show: vi.fn() },
		updates: {
			getStatus: updGetStatus,
			check: updCheck,
			download: updDownload,
			install: updInstall,
			onStatus: updOnStatus,
		},
	},
}));

// The unified shell's scope switcher calls useNavigate + useWorkspaceQuery, which
// need a router context these unit renders don't provide. Preserve every other
// export and stub navigation to a no-op (workspaces resolve empty on their own).
vi.mock("@tanstack/react-router", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-router")>();
	return { ...actual, useNavigate: () => vi.fn() };
});

import { GlobalSettingsForm } from "./GlobalSettingsForm";

function renderForm() {
	const qc = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	render(
		<QueryClientProvider client={qc}>
			<GlobalSettingsForm />
		</QueryClientProvider>,
	);
	return qc;
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
// so edits survive navigation and one save bar commits the whole global config.
// Section names follow the variant-B cut: sections are named for what a setting
// acts on rather than for the shape of the config file.
async function goToSection(name: "Every agent" | "While work runs" | "Cleaning up" | "Simulators" | "This Mac") {
	await userEvent.click(await screen.findByRole("button", { name: new RegExp(`^${name}`) }));
	await openRows();
}

async function chooseOption(trigger: HTMLElement, optionName: string) {
	await userEvent.click(trigger);
	await userEvent.click(await screen.findByRole("option", { name: optionName }));
}

const promptsPayload = {
	data: {
		prompts: [
			{ kind: "orchestrator", default: "Orchestrator base", override: null },
			{ kind: "worker", default: "Worker base", override: null },
			{ kind: "reviewer", default: "Reviewer base", override: null },
		],
	},
	error: undefined,
};
const PROXYMAN_CA = "~/Library/Application Support/com.proxyman.NSProxy/app-data/proxyman-ca.pem";
// The saved global list: the shipped Proxyman CA (present on this Mac) plus a
// Charles CA that is not installed here.
const simTrustPayload = {
	data: {
		caFiles: [PROXYMAN_CA, "/opt/charles/charles-ca.pem"],
		defaultCaFiles: [PROXYMAN_CA],
		found: [true, false],
	},
	error: undefined,
};

const templatesPayload = {
	data: {
		templates: [
			{ name: "ci-failing", default: "CI is failing on {{.Branch}}", placeholders: ["{{.Branch}}"], override: null },
		],
	},
	error: undefined,
};

// GlobalSettingsForm's hook fires a query per slice (prompts, templates,
// spawn-confirm, auto-nudge, reclaim) plus the migration availability probe, all
// on apiClient.GET. getMock branches on the requested path so each slice seeds
// from its own payload instead of one shared blob. `promptOverrides` lets a test
// pre-seed an override so the reset→DELETE path can be exercised.
function mockGet(importPayload: unknown, promptOverrides: Record<string, string> = {}) {
	const prompts = {
		data: {
			prompts: promptsPayload.data.prompts.map((p) => ({ ...p, override: promptOverrides[p.kind] ?? null })),
		},
		error: undefined,
	};
	getMock.mockImplementation(async (path: string) => {
		switch (path) {
			case "/api/v1/settings/prompts":
				return prompts;
			case "/api/v1/settings/message-templates":
				return templatesPayload;
			case "/api/v1/settings/spawn-confirm":
				return { data: { enabled: true }, error: undefined };
			case "/api/v1/settings/auto-nudge":
				return { data: { enabled: false }, error: undefined };
			case "/api/v1/settings/response-language":
				return { data: { language: "English" }, error: undefined };
			case "/api/v1/settings/reclaim":
				return { data: { enabled: true, graceMinutes: 1440, artifactsEnabled: true }, error: undefined };
			case "/api/v1/settings/wiki":
				return { data: { vaultPath: "", harness: "" }, error: undefined };
			case "/api/v1/settings/sim-trust":
				return simTrustPayload;
			case "/api/v1/import":
				return importPayload;
			default:
				return { data: {}, error: undefined };
		}
	});
}

beforeEach(() => {
	for (const m of [getMock, postMock, putMock, deleteMock, getMigration, setMigration, getUpdate, setUpdate])
		m.mockReset();
	getMigration.mockResolvedValue({ status: "pending" });
	mockGet({ data: { available: true, legacyRoot: "/home/u/.agent-orchestrator" }, error: undefined });
	postMock.mockResolvedValue({ data: { report: { projectsImported: 2, projectsSkipped: 1 } }, error: undefined });
	putMock.mockResolvedValue({ data: {}, error: undefined });
	deleteMock.mockResolvedValue({ data: {}, error: undefined });
	setMigration.mockResolvedValue(undefined);
	getUpdate.mockResolvedValue({ enabled: true, channel: "latest", nightlyAck: false });
	setUpdate.mockResolvedValue(undefined);
	updGetStatus.mockResolvedValue({ state: "idle" });
	updCheck.mockResolvedValue(undefined);
	updDownload.mockResolvedValue(undefined);
	updInstall.mockResolvedValue(undefined);
	updOnStatus.mockReturnValue(() => undefined);
	getVersion.mockResolvedValue("1.4.0");
});

describe("GlobalSettingsForm", () => {
	it("shows Every agent by default and This Mac on demand", async () => {
		renderForm();
		await openRows();
		// Every agent is the default section: one row per prompt kind, collapsed.
		expect(await screen.findByRole("button", { name: /^Worker/ })).toBeInTheDocument();
		await goToSection("This Mac");
		expect(await screen.findByRole("button", { name: /^Updates/ })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^Migration/ })).toBeInTheDocument();
	});

	it("edits a system prompt in the drawer and saves it via one bar (PUT)", async () => {
		renderForm();
		// The prompt rows only exist once the prompts query resolves.
		await screen.findByRole("button", { name: /^Orchestrator/ });
		await openRows();
		await userEvent.click(await screen.findByRole("button", { name: "Edit Orchestrator" }));
		const drawer = await screen.findByRole("dialog");
		const textbox = within(drawer).getByRole("textbox") as HTMLTextAreaElement;
		await waitFor(() => expect(textbox.value).toBe("Orchestrator base"));
		await userEvent.clear(textbox);
		await userEvent.type(textbox, "custom orchestrator base");
		await userEvent.click(screen.getByRole("button", { name: "Done" }));

		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/prompts/{kind}", {
				params: { path: { kind: "orchestrator" } },
				body: { base: "custom orchestrator base" },
			}),
		);
		expect(await screen.findByText("Saved.")).toBeInTheDocument();
	});

	it("resetting an overridden prompt to default saves a DELETE", async () => {
		mockGet({ data: { available: true, legacyRoot: "/x" }, error: undefined }, { orchestrator: "an override" });
		renderForm();
		await screen.findByRole("button", { name: /^Orchestrator/ });
		await openRows();
		// The overridden row reads Customised; open its drawer and reset to default.
		await userEvent.click(await screen.findByRole("button", { name: "Edit Orchestrator" }));
		const drawer = await screen.findByRole("dialog");
		await waitFor(() => expect((within(drawer).getByRole("textbox") as HTMLTextAreaElement).value).toBe("an override"));
		await userEvent.click(within(drawer).getByRole("button", { name: "Reset to default" }));
		await userEvent.click(screen.getByRole("button", { name: "Done" }));

		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
		await waitFor(() =>
			expect(deleteMock).toHaveBeenCalledWith("/api/v1/settings/prompts/{kind}", {
				params: { path: { kind: "orchestrator" } },
			}),
		);
	});

	it("routes the response-language default through the save bar (PUT response-language)", async () => {
		renderForm();
		await openRows();
		// Every agent is the default section; the language row sits at its top.
		const language = await screen.findByRole("combobox", { name: "Default response language" });
		expect(language).toHaveTextContent("English");
		await chooseOption(language, "Thai");
		await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/response-language", { body: { language: "Thai" } }),
		);
	});

	it("routes the Auto-send toggle through the save bar (PUT auto-nudge)", async () => {
		renderForm();
		await goToSection("While work runs");
		const toggle = await screen.findByLabelText("Enabled by default");
		expect(toggle).not.toBeChecked();
		await userEvent.click(toggle);
		// The toggle no longer self-saves: it dirties the shared bar.
		await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/auto-nudge", { body: { enabled: true } }),
		);
	});

	// Auto-reclaim deletes worktrees silently, so its knobs must be findable and
	// switchable off from the UI — including the newer build-output clearing,
	// which is the part that actually deletes files a rebuild has to recreate.
	it("routes the build-output clearing toggle through the save bar (PUT reclaim)", async () => {
		renderForm();
		await goToSection("Cleaning up");
		const toggle = await screen.findByRole("combobox", { name: "Clear build output" });
		expect(toggle).toHaveTextContent("Enabled");
		await chooseOption(toggle, "Disabled");
		await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/reclaim", {
				body: { enabled: true, graceMinutes: 1440, artifactsEnabled: false },
			}),
		);
	});

	// The whole feature must be switchable off, and doing so must not quietly
	// drop the other reclaim settings from the request body.
	it("switches auto-reclaim off without dropping the other reclaim fields", async () => {
		renderForm();
		await goToSection("Cleaning up");
		const toggle = await screen.findByRole("combobox", { name: "Auto-reclaim" });
		expect(toggle).toHaveTextContent("Enabled");
		await chooseOption(toggle, "Disabled");
		await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/reclaim", {
				body: { enabled: false, graceMinutes: 1440, artifactsEnabled: true },
			}),
		);
	});

	it("routes the wiki vault path through the save bar (PUT settings/wiki)", async () => {
		renderForm();
		await goToSection("This Mac");
		await userEvent.type(await screen.findByLabelText("Vault folder"), "~/Notes");
		await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/wiki", { body: { vaultPath: "~/Notes" } }),
		);
	});

	// The sidebar's Wiki row exists only while a vault is configured, and it
	// reads the wiki STATUS query without polling — so the save has to push it,
	// or clearing the path leaves a row pointing at nothing.
	it("pushes the wiki status so the sidebar row appears and disappears with the path", async () => {
		const qc = renderForm();
		const invalidate = vi.spyOn(qc, "invalidateQueries");
		await goToSection("This Mac");
		await userEvent.type(await screen.findByLabelText("Vault folder"), "~/Notes");
		await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));
		await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["wiki", "status"] }));
	});

	it("changes the update channel and saves it through the bar", async () => {
		renderForm();
		await goToSection("This Mac");
		await screen.findByText("Updates");
		expect(screen.queryByText(/Nightly builds are cut every day/i)).not.toBeInTheDocument();

		await chooseOption(screen.getByRole("combobox", { name: "Update channel" }), "Nightly (pre-release)");
		expect(await screen.findByText(/Nightly builds are cut every day/i)).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
		await waitFor(() =>
			expect(setUpdate).toHaveBeenCalledWith(
				expect.objectContaining({ channel: "nightly", enabled: true, nightlyAck: true }),
			),
		);
	});

	it("loads the simulator root CAs one per line and says which saved files are on this Mac", async () => {
		renderForm();
		await goToSection("Simulators");
		const field = await screen.findByLabelText("Simulator root CAs");
		await waitFor(() => expect(field).toHaveValue(`${PROXYMAN_CA}\n/opt/charles/charles-ca.pem`));
		const saved = screen.getByRole("list", { name: "Saved root CAs on this Mac" });
		const [proxyman, charles] = within(saved).getAllByRole("listitem");
		expect(proxyman).toHaveTextContent(PROXYMAN_CA);
		expect(proxyman).toHaveTextContent("Found");
		expect(charles).toHaveTextContent("/opt/charles/charles-ca.pem");
		expect(charles).toHaveTextContent("Not on this Mac, skipped");
		// Seeding is not an edit: the bar stays idle.
		expect(screen.queryByRole("button", { name: "Save changes" })).not.toBeInTheDocument();
	});

	it("saves edited simulator root CAs through the bar (PUT sim-trust) and shows the saved list", async () => {
		putMock.mockImplementation(async (path: string) =>
			path === "/api/v1/settings/sim-trust"
				? { data: { caFiles: ["/certs/a.pem", "~/b.pem"], defaultCaFiles: [PROXYMAN_CA], found: [false, true] } }
				: { data: {}, error: undefined },
		);
		renderForm();
		await goToSection("Simulators");
		const field = await screen.findByLabelText("Simulator root CAs");
		await waitFor(() => expect(field).toHaveValue(`${PROXYMAN_CA}\n/opt/charles/charles-ca.pem`));
		// Stray whitespace and blank lines never reach the daemon, which refuses them.
		fireEvent.change(field, { target: { value: "  /certs/a.pem\n\n~/b.pem  \n" } });
		await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));

		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/sim-trust", {
				body: { caFiles: ["/certs/a.pem", "~/b.pem"] },
			}),
		);
		expect(await screen.findByText("Saved.")).toBeInTheDocument();
		// The field shows the list as stored, and the found flags are the saved list's.
		expect(field).toHaveValue("/certs/a.pem\n~/b.pem");
		const [a, b] = within(screen.getByRole("list", { name: "Saved root CAs on this Mac" })).getAllByRole("listitem");
		expect(a).toHaveTextContent("Not on this Mac, skipped");
		expect(b).toHaveTextContent("Found");
	});

	it("restores the shipped default list of simulator root CAs", async () => {
		renderForm();
		await goToSection("Simulators");
		const field = await screen.findByLabelText("Simulator root CAs");
		await waitFor(() => expect(field).toHaveValue(`${PROXYMAN_CA}\n/opt/charles/charles-ca.pem`));
		await userEvent.click(screen.getByRole("button", { name: "Restore default" }));
		expect(field).toHaveValue(PROXYMAN_CA);
		// Already at the default: nothing left to restore.
		expect(screen.getByRole("button", { name: "Restore default" })).toBeDisabled();

		await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/sim-trust", { body: { caFiles: [PROXYMAN_CA] } }),
		);
	});

	it("saves an emptied simulator root-CA list as trusting nothing", async () => {
		renderForm();
		await goToSection("Simulators");
		const field = await screen.findByLabelText("Simulator root CAs");
		await waitFor(() => expect(field).not.toHaveValue(""));
		await userEvent.clear(field);
		await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));
		await waitFor(() => expect(putMock).toHaveBeenCalledWith("/api/v1/settings/sim-trust", { body: { caFiles: [] } }));
	});

	it("shows the daemon's refusal of a simulator root-CA path and keeps the edit", async () => {
		putMock.mockImplementation(async (path: string) =>
			path === "/api/v1/settings/sim-trust"
				? {
						data: undefined,
						error: { message: 'caFiles[0]: "certs/a.pem" must be absolute or start with ~/' },
					}
				: { data: {}, error: undefined },
		);
		renderForm();
		await goToSection("Simulators");
		const field = await screen.findByLabelText("Simulator root CAs");
		await waitFor(() => expect(field).not.toHaveValue(""));
		fireEvent.change(field, { target: { value: "certs/a.pem" } });
		await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));

		expect(await screen.findByText('caFiles[0]: "certs/a.pem" must be absolute or start with ~/')).toBeInTheDocument();
		expect(field).toHaveValue("certs/a.pem");
		expect(screen.getByRole("button", { name: "Save changes" })).toBeInTheDocument();
	});

	it("shows migration status and the available legacy root", async () => {
		renderForm();
		await goToSection("This Mac");
		expect(await screen.findByText("Not migrated yet")).toBeInTheDocument();
		expect(await screen.findByText("/home/u/.agent-orchestrator")).toBeInTheDocument();
	});

	it("Run migration imports and marks completed", async () => {
		renderForm();
		await goToSection("This Mac");
		await userEvent.click(await screen.findByRole("button", { name: "Run migration" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/import"));
		expect(setMigration).toHaveBeenCalledWith(expect.objectContaining({ status: "completed" }));
		expect(await screen.findByText("Migration complete.")).toBeInTheDocument();
	});

	it("lets a declined user re-run the migration", async () => {
		getMigration.mockResolvedValue({ status: "declined", lastAttemptAt: "2026-06-01T00:00:00.000Z" });
		renderForm();
		await goToSection("This Mac");
		expect(await screen.findByText("Declined")).toBeInTheDocument();
		const btn = await screen.findByRole("button", { name: "Run migration" });
		expect(btn).toBeEnabled();
		await userEvent.click(btn);
		await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/import"));
	});

	it("disables Run when no legacy install is available", async () => {
		mockGet({ data: { available: false, legacyRoot: "" }, error: undefined });
		renderForm();
		await goToSection("This Mac");
		expect(await screen.findByText("None found")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Run migration" })).toBeDisabled();
	});

	it("shows the current app version", async () => {
		renderForm();
		await goToSection("This Mac");
		expect(await screen.findByText("v1.4.0")).toBeInTheDocument();
	});

	it("Check for updates triggers a manual check", async () => {
		renderForm();
		await goToSection("This Mac");
		await userEvent.click(await screen.findByRole("button", { name: "Check for updates" }));
		expect(updCheck).toHaveBeenCalled();
	});

	it("offers an Update button when an update is available and downloads it", async () => {
		let emit: (s: { state: string; version?: string }) => void = () => undefined;
		updOnStatus.mockImplementation((cb: (s: unknown) => void) => {
			emit = cb as typeof emit;
			return () => undefined;
		});
		renderForm();
		await goToSection("This Mac");
		await screen.findByRole("button", { name: "Check for updates" });
		act(() => emit({ state: "available", version: "1.2.3" }));
		await userEvent.click(await screen.findByRole("button", { name: "Update to v1.2.3" }));
		expect(updDownload).toHaveBeenCalled();
	});

	it("offers Restart & install once downloaded and installs it", async () => {
		let emit: (s: { state: string; version?: string }) => void = () => undefined;
		updOnStatus.mockImplementation((cb: (s: unknown) => void) => {
			emit = cb as typeof emit;
			return () => undefined;
		});
		renderForm();
		await goToSection("This Mac");
		await screen.findByRole("button", { name: "Check for updates" });
		act(() => emit({ state: "downloaded", version: "1.2.3" }));
		await userEvent.click(await screen.findByRole("button", { name: /Restart & install/ }));
		expect(updInstall).toHaveBeenCalled();
	});

	it("a failed import surfaces the error and marks failed", async () => {
		postMock.mockResolvedValue({ data: undefined, error: { message: "disk full" } });
		renderForm();
		await goToSection("This Mac");
		await userEvent.click(await screen.findByRole("button", { name: "Run migration" }));
		expect(await screen.findByText(/disk full/i)).toBeInTheDocument();
		expect(setMigration).toHaveBeenCalledWith(expect.objectContaining({ status: "failed", error: "disk full" }));
	});
});
