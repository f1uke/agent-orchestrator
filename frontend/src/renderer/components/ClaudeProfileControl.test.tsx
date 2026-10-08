import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { ClaudeProfileControl } from "./ClaudeProfileControl";

const { getMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, PUT: putMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") =>
		typeof error === "object" && error !== null && "message" in error
			? String((error as { message: unknown }).message)
			: fallback,
}));

const active: WorkspaceSession = {
	id: "sess-1",
	workspaceId: "proj-1",
	workspaceName: "my-app",
	title: "do the thing",
	provider: "claude-code",
	kind: "worker",
	branch: "ao/sess-1",
	status: "working",
	updatedAt: "2026-10-08T00:00:00Z",
	activity: { state: "active", lastActivityAt: "2026-10-08T00:00:00Z" },
	prs: [],
};
const idle: WorkspaceSession = { ...active, activity: { state: "idle", lastActivityAt: "2026-10-08T00:00:00Z" } };

function renderControl(session: WorkspaceSession) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	const invalidate = vi.spyOn(queryClient, "invalidateQueries");
	render(
		<QueryClientProvider client={queryClient}>
			<ClaudeProfileControl session={session} />
		</QueryClientProvider>,
	);
	return { invalidate };
}

async function pickProfile(name: string) {
	await userEvent.click(await screen.findByRole("button", { name: /^Claude profile:/ }));
	await userEvent.click(await screen.findByRole("menuitemradio", { name }));
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({
		data: {
			profiles: [
				{ name: "Subscription", settingsFile: "", builtin: true },
				{ name: "OmniRoute", settingsFile: "~/.claude/settings-omniroute.json", builtin: true },
			],
		},
		error: undefined,
	});
	putMock.mockReset().mockResolvedValue({ data: { session: {}, restart: "pending" }, error: undefined });
});

describe("ClaudeProfileControl", () => {
	it("shows the session's profile, with Subscription for an unset one", async () => {
		renderControl(active);
		expect(await screen.findByRole("button", { name: "Claude profile: Subscription" })).toHaveTextContent(
			"Subscription",
		);
	});

	it("asks to restart a busy session when idle, then sends the switch with a restart", async () => {
		const { invalidate } = renderControl(active);
		await pickProfile("OmniRoute");

		expect(await screen.findByRole("dialog", { name: "Switch to OmniRoute?" })).toHaveTextContent(
			/when the current turn ends/,
		);
		expect(screen.queryByRole("button", { name: "Restart now" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Restart when idle" }));

		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/claude-profile", {
				params: { path: { sessionId: "sess-1" } },
				body: { profile: "OmniRoute", restart: true },
			}),
		);
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(invalidate).toHaveBeenCalledWith({ queryKey: workspaceQueryKey });
	});

	it("offers Restart now for an idle session", async () => {
		renderControl(idle);
		await pickProfile("OmniRoute");
		await userEvent.click(await screen.findByRole("button", { name: "Restart now" }));
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/claude-profile", {
				params: { path: { sessionId: "sess-1" } },
				body: { profile: "OmniRoute", restart: true },
			}),
		);
	});

	it("switches without restarting", async () => {
		renderControl({ ...idle, claudeProfile: "OmniRoute" });
		await pickProfile("Subscription");
		await userEvent.click(await screen.findByRole("button", { name: "Switch without restarting" }));
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/claude-profile", {
				params: { path: { sessionId: "sess-1" } },
				body: { profile: "Subscription", restart: false },
			}),
		);
	});

	it("does nothing when the current profile is picked again", async () => {
		renderControl({ ...idle, claudeProfile: "OmniRoute" });
		await pickProfile("OmniRoute");
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("keeps the dialog open with the daemon's refusal", async () => {
		putMock.mockResolvedValue({
			data: undefined,
			error: { code: "CLAUDE_PROFILE_SETTINGS_INVALID", message: "settings file not found" },
		});
		renderControl(idle);
		await pickProfile("OmniRoute");
		await userEvent.click(await screen.findByRole("button", { name: "Restart now" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("settings file not found");
		expect(screen.getByRole("dialog")).toBeInTheDocument();
	});

	it("says a restart is pending", async () => {
		renderControl({ ...active, claudeProfile: "OmniRoute", restartPending: true });
		expect(await screen.findByRole("button", { name: "Claude profile: OmniRoute, restart pending" })).toHaveTextContent(
			"restart pending",
		);
	});

	it("renders nothing for an agent that is not Claude Code", () => {
		const { container } = render(
			<QueryClientProvider client={new QueryClient()}>
				<ClaudeProfileControl session={{ ...active, provider: "codex" }} />
			</QueryClientProvider>,
		);
		expect(container).toBeEmptyDOMElement();
		expect(getMock).not.toHaveBeenCalled();
	});
});
