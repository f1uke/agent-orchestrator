import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SessionSuggestionCard, SkillSuggestionCard, SuggestedSessionDialog } from "./ChatSuggestion";

const { getMock, postMock, navigateMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	navigateMock: vi.fn(),
}));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigateMock }));
vi.mock("../hooks/useSessionJiraContext", () => ({
	useJiraSearch: () => ({ data: [], isFetching: false, isError: false, error: null }),
}));
vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: (...args: unknown[]) => getMock(...args),
		POST: (...args: unknown[]) => postMock(...args),
	},
	apiErrorMessage: (_error: unknown, fallback = "Request failed") => fallback,
}));

beforeEach(() => {
	navigateMock.mockReset();
	postMock.mockReset().mockResolvedValue({ data: { session: { id: "new-1" } }, error: undefined });
	getMock.mockReset().mockImplementation(async (path: string) => {
		if (path === "/api/v1/agents") {
			const claude = { id: "claude-code", label: "Claude Code", authStatus: "authorized" };
			return { data: { supported: [claude], installed: [claude], authorized: [claude] }, error: undefined };
		}
		if (path === "/api/v1/settings/claude-profiles") return { data: { profiles: [] }, error: undefined };
		if (path === "/api/v1/projects/{id}/branches") return { data: { branches: [] }, error: undefined };
		return {
			data: {
				status: "ok",
				project: { id: "proj-1", defaultBranch: "main", config: { worker: { agent: "claude-code" } } },
			},
			error: undefined,
		};
	});
});

const SESSION = { title: "Fix the flaky Esc test", prompt: "Find the race. Fix it." };

describe("SessionSuggestionCard", () => {
	it("opens the form with the suggestion and does not prompt", async () => {
		const onOpen = vi.fn();
		const onPrompt = vi.fn();
		render(<SessionSuggestionCard live onOpen={onOpen} onPrompt={onPrompt} suggestion={SESSION} />);
		expect(screen.getByText("Start a new session")).toBeInTheDocument();
		expect(screen.getByText("Fix the flaky Esc test")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Open new session…" }));
		expect(onOpen).toHaveBeenCalledExactlyOnceWith(SESSION);
		expect(onPrompt).not.toHaveBeenCalled();
	});

	it("hands Do it here to the composer, not to the form", async () => {
		const onOpen = vi.fn();
		const onPrompt = vi.fn();
		render(<SessionSuggestionCard live onOpen={onOpen} onPrompt={onPrompt} suggestion={SESSION} />);
		await userEvent.click(screen.getByRole("button", { name: "Do it here" }));
		expect(onPrompt).toHaveBeenCalledExactlyOnceWith("Do it here: Fix the flaky Esc test");
		expect(onOpen).not.toHaveBeenCalled();
	});

	it("keeps an older card readable but dead", async () => {
		const onOpen = vi.fn();
		const onPrompt = vi.fn();
		render(<SessionSuggestionCard live={false} onOpen={onOpen} onPrompt={onPrompt} suggestion={SESSION} />);
		for (const name of ["Open new session…", "Do it here"]) {
			const button = screen.getByRole("button", { name });
			expect(button).toHaveAttribute("aria-disabled", "true");
			await userEvent.click(button);
		}
		expect(onOpen).not.toHaveBeenCalled();
		expect(onPrompt).not.toHaveBeenCalled();
	});
});

describe("SkillSuggestionCard", () => {
	const suggestion = { name: "verify", reason: "Drives the app" };

	it("puts /<name> and a space in the composer", async () => {
		const onPrompt = vi.fn();
		render(<SkillSuggestionCard live onPrompt={onPrompt} suggestion={suggestion} />);
		expect(screen.getByText("Skill: /verify")).toBeInTheDocument();
		expect(screen.getByText("Drives the app")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Use /verify" }));
		expect(onPrompt).toHaveBeenCalledExactlyOnceWith("/verify ");
	});

	it("keeps an older card dead", async () => {
		const onPrompt = vi.fn();
		render(<SkillSuggestionCard live={false} onPrompt={onPrompt} suggestion={suggestion} />);
		const button = screen.getByRole("button", { name: "Use /verify" });
		expect(button).toHaveAttribute("aria-disabled", "true");
		await userEvent.click(button);
		expect(onPrompt).not.toHaveBeenCalled();
	});
});

describe("SuggestedSessionDialog", () => {
	function renderDialog(onClose = vi.fn()) {
		render(
			<QueryClientProvider client={new QueryClient()}>
				<SuggestedSessionDialog onClose={onClose} projectId="proj-1" suggestion={SESSION} />
			</QueryClientProvider>,
		);
		return onClose;
	}

	it("opens the New task form filled in for this project and spawns nothing by itself", async () => {
		renderDialog();
		await waitFor(() => expect(screen.getByLabelText("Title")).toHaveValue("Fix the flaky Esc test"));
		expect(screen.getByLabelText("Brief")).toHaveValue("Find the race. Fix it.");
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(postMock).not.toHaveBeenCalled();
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("closing it creates nothing", async () => {
		const onClose = renderDialog();
		await screen.findByLabelText("Title");
		await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
		expect(onClose).toHaveBeenCalled();
		expect(postMock).not.toHaveBeenCalled();
	});

	it("goes to the new session only after the human presses Start", async () => {
		renderDialog();
		await screen.findByLabelText("Title");
		await waitFor(() => expect(screen.getAllByText("Claude Code").length).toBeGreaterThan(0));
		await userEvent.click(screen.getByRole("button", { name: "Start now" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		await waitFor(() =>
			expect(navigateMock).toHaveBeenCalledWith({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId: "proj-1", sessionId: "new-1" },
			}),
		);
	}, 10_000);
});
