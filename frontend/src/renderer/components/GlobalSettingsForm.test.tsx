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

// The two-pane shell shows one section at a time; navigate to a section's nav
// button before interacting with its fields. The draft lives above the sections
// so edits survive navigation and one save bar commits the whole global config.
async function goToSection(name: "Prompts" | "Messages" | "Automation" | "System") {
	await userEvent.click(await screen.findByRole("button", { name }));
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
			case "/api/v1/settings/reclaim":
				return { data: { enabled: true, graceMinutes: 15 }, error: undefined };
			case "/api/v1/settings/evidence-retention":
				return { data: { enabled: true, maxAgeDays: 30 }, error: undefined };
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
	it("shows Prompts by default and the System section on demand", async () => {
		renderForm();
		// Prompts is the default section: the per-kind editor rows are visible.
		expect(await screen.findByRole("button", { name: "Edit Orchestrator" })).toBeInTheDocument();
		await goToSection("System");
		expect(await screen.findByText("Updates")).toBeInTheDocument();
		expect(screen.getByText("Migration")).toBeInTheDocument();
	});

	it("edits a system prompt in the drawer and saves it via one bar (PUT)", async () => {
		renderForm();
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
		// The overridden row reads Customized; open its drawer and reset to default.
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

	it("routes the Auto-send toggle through the save bar (PUT auto-nudge)", async () => {
		renderForm();
		await goToSection("Automation");
		const toggle = await screen.findByLabelText("Enabled by default");
		expect(toggle).not.toBeChecked();
		await userEvent.click(toggle);
		// The toggle no longer self-saves: it dirties the shared bar.
		await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/auto-nudge", { body: { enabled: true } }),
		);
	});

	it("routes the evidence-retention TTL through the save bar (PUT evidence-retention)", async () => {
		renderForm();
		await goToSection("Automation");
		const days = await screen.findByLabelText("Delete evidence older than (days)");
		expect(days).toHaveValue(30);
		fireEvent.change(days, { target: { value: "7" } });
		await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/evidence-retention", {
				body: { enabled: true, maxAgeDays: 7 },
			}),
		);
	});

	it("runs the manual evidence purge (POST sweep) and reports the result", async () => {
		postMock.mockReset().mockImplementation(async (path: string) => {
			if (String(path).includes("evidence-retention/sweep")) {
				return { data: { purged: 2, freedBytes: 2048 }, error: undefined };
			}
			return { data: {}, error: undefined };
		});
		renderForm();
		await goToSection("Automation");
		await userEvent.click(await screen.findByRole("button", { name: "Purge now" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/settings/evidence-retention/sweep", {}));
		expect(await screen.findByText(/Purged 2 items · freed 2 KB\./)).toBeInTheDocument();
	});

	it("changes the update channel and saves it through the bar", async () => {
		renderForm();
		await goToSection("System");
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

	it("shows migration status and the available legacy root", async () => {
		renderForm();
		await goToSection("System");
		expect(await screen.findByText("Not migrated yet")).toBeInTheDocument();
		expect(await screen.findByText("/home/u/.agent-orchestrator")).toBeInTheDocument();
	});

	it("Run migration imports and marks completed", async () => {
		renderForm();
		await goToSection("System");
		await userEvent.click(await screen.findByRole("button", { name: "Run migration" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/import"));
		expect(setMigration).toHaveBeenCalledWith(expect.objectContaining({ status: "completed" }));
		expect(await screen.findByText("Migration complete.")).toBeInTheDocument();
	});

	it("lets a declined user re-run the migration", async () => {
		getMigration.mockResolvedValue({ status: "declined", lastAttemptAt: "2026-06-01T00:00:00.000Z" });
		renderForm();
		await goToSection("System");
		expect(await screen.findByText("Declined")).toBeInTheDocument();
		const btn = await screen.findByRole("button", { name: "Run migration" });
		expect(btn).toBeEnabled();
		await userEvent.click(btn);
		await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/import"));
	});

	it("disables Run when no legacy install is available", async () => {
		mockGet({ data: { available: false, legacyRoot: "" }, error: undefined });
		renderForm();
		await goToSection("System");
		expect(await screen.findByText("None found")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Run migration" })).toBeDisabled();
	});

	it("shows the current app version", async () => {
		renderForm();
		await goToSection("System");
		expect(await screen.findByText("v1.4.0")).toBeInTheDocument();
	});

	it("Check for updates triggers a manual check", async () => {
		renderForm();
		await goToSection("System");
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
		await goToSection("System");
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
		await goToSection("System");
		await screen.findByRole("button", { name: "Check for updates" });
		act(() => emit({ state: "downloaded", version: "1.2.3" }));
		await userEvent.click(await screen.findByRole("button", { name: /Restart & install/ }));
		expect(updInstall).toHaveBeenCalled();
	});

	it("a failed import surfaces the error and marks failed", async () => {
		postMock.mockResolvedValue({ data: undefined, error: { message: "disk full" } });
		renderForm();
		await goToSection("System");
		await userEvent.click(await screen.findByRole("button", { name: "Run migration" }));
		expect(await screen.findByText(/disk full/i)).toBeInTheDocument();
		expect(setMigration).toHaveBeenCalledWith(expect.objectContaining({ status: "failed", error: "disk full" }));
	});
});
