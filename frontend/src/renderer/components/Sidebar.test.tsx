import { SidebarProvider } from "@/components/ui/sidebar";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Sidebar } from "./Sidebar";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { agentsQueryKey } from "../hooks/useAgentsQuery";
import { useUiStore } from "../stores/ui-store";

const { getMock, navigateMock, mockParams, renameSessionMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	navigateMock: vi.fn(),
	// Drives useSelection: which project/session the URL points at. Reset per test;
	// the active-glow tests set these to simulate the Dashboard vs Orchestrator route.
	mockParams: {
		projectId: undefined as string | undefined,
		sessionId: undefined as string | undefined,
	},
	renameSessionMock: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("../lib/rename-session", () => ({ renameSession: renameSessionMock }));

vi.mock("@tanstack/react-router", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-router")>();
	return {
		...actual,
		useNavigate: () => navigateMock,
		useParams: () => ({ projectId: mockParams.projectId, sessionId: mockParams.sessionId }),
		useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => unknown }) =>
			select({ location: { pathname: mockParams.projectId ? `/projects/${mockParams.projectId}` : "/" } }),
	};
});

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (error: unknown) => {
		if (error instanceof Error) return error.message;
		if (typeof error === "object" && error !== null && "message" in error && typeof error.message === "string") {
			return error.message;
		}
		return "Request failed";
	},
}));

const workspace: WorkspaceSummary = {
	id: "proj-1",
	name: "Project One",
	path: "/repo/project-one",
	sessions: [],
};

const session: WorkspaceSession = {
	id: "proj-1-1",
	workspaceId: "proj-1",
	workspaceName: "Project One",
	title: "fix login",
	provider: "claude-code",
	kind: "worker",
	branch: "session/proj-1-1",
	status: "working",
	updatedAt: "2026-06-30T00:00:00Z",
	prs: [],
};

// A live orchestrator session; backs the Orchestrator button's active glow when
// its route is open (isOrchestratorSession + sessionIsActive).
const orchestratorSession: WorkspaceSession = {
	id: "proj-1-orchestrator",
	workspaceId: "proj-1",
	workspaceName: "Project One",
	title: "orchestrator",
	provider: "claude-code",
	kind: "orchestrator",
	branch: "orchestrator/proj-1",
	status: "working",
	updatedAt: "2026-06-30T00:00:00Z",
	prs: [],
};

type CreateProjectInput = {
	path: string;
	workerAgent: string;
	orchestratorAgent: string;
	trackerIntake?: unknown;
	asWorkspace?: boolean;
};
type CreateProjectHandler = (input: CreateProjectInput) => Promise<void>;
type RemoveProjectHandler = (projectId: string) => Promise<void>;

function renderSidebar({
	onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler,
	onRemoveProject = vi.fn().mockResolvedValue(undefined) as RemoveProjectHandler,
	seedAgents = true,
	workspaces = [workspace],
}: {
	onCreateProject?: CreateProjectHandler;
	onRemoveProject?: RemoveProjectHandler;
	seedAgents?: boolean;
	workspaces?: WorkspaceSummary[];
} = {}) {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	if (seedAgents) {
		queryClient.setQueryData(agentsQueryKey, {
			supported: [
				{ id: "claude-code", label: "Claude Code" },
				{ id: "codex", label: "Codex" },
			],
			installed: [
				{ id: "claude-code", label: "Claude Code" },
				{ id: "codex", label: "Codex" },
			],
			authorized: [
				{ id: "claude-code", label: "Claude Code", authStatus: "authorized" },
				{ id: "codex", label: "Codex", authStatus: "authorized" },
			],
		});
	}
	const tree = (ws: WorkspaceSummary[]) => (
		<QueryClientProvider client={queryClient}>
			<SidebarProvider>
				<Sidebar
					daemonStatus={{ state: "running" }}
					onCreateProject={onCreateProject}
					onRemoveProject={onRemoveProject}
					workspaces={ws}
				/>
			</SidebarProvider>
		</QueryClientProvider>
	);
	const result = render(tree(workspaces));
	// Re-render the SAME tree with new workspace data, so a test can drive a live
	// state transition (e.g. the orchestrator going busy → idle) the way a refetch
	// does, instead of mounting a second sidebar.
	rerenderWorkspaces = (ws) => result.rerender(tree(ws));
	return onRemoveProject;
}

let rerenderWorkspaces: (workspaces: WorkspaceSummary[]) => void = () => undefined;

async function chooseOption(trigger: HTMLElement, optionName: string) {
	await userEvent.click(trigger);
	await userEvent.click(await screen.findByRole("option", { name: optionName }));
}

beforeEach(() => {
	getMock.mockReset();
	getMock.mockResolvedValue({
		data: {
			supported: [
				{ id: "claude-code", label: "Claude Code" },
				{ id: "codex", label: "Codex" },
			],
			installed: [
				{ id: "claude-code", label: "Claude Code" },
				{ id: "codex", label: "Codex" },
			],
			authorized: [
				{ id: "claude-code", label: "Claude Code", authStatus: "authorized" },
				{ id: "codex", label: "Codex", authStatus: "authorized" },
			],
		},
		error: undefined,
	});
	navigateMock.mockReset();
	renameSessionMock.mockReset().mockResolvedValue(undefined);
	mockParams.projectId = undefined;
	mockParams.sessionId = undefined;
	localStorage.clear();
	useUiStore.setState({ collapsedProjectIds: new Set(), projectOrder: [] });
	vi.spyOn(window, "confirm").mockReturnValue(true);
	vi.spyOn(window, "alert").mockImplementation(() => undefined);
});

afterEach(() => {
	vi.restoreAllMocks();
});

describe("Sidebar", () => {
	it("orders sessions by state: working → needs → review → merge", () => {
		const mk = (id: string, title: string, status: WorkspaceSession["status"]): WorkspaceSession => ({
			...session,
			id,
			title,
			status,
		});
		renderSidebar({
			workspaces: [
				{
					...workspace,
					// Provided out of order — the sidebar sorts them into board-lane flow.
					sessions: [
						mk("s-merge", "ship-it", "mergeable"),
						mk("s-review", "in-review", "pr_open"),
						mk("s-needs", "needs-me", "needs_input"),
						mk("s-working", "busy", "working"),
					],
				},
			],
		});

		const order = screen
			.getAllByRole("button", { name: /^Open (busy|needs-me|in-review|ship-it)$/ })
			.map((b) => b.getAttribute("aria-label"));
		expect(order).toEqual(["Open busy", "Open needs-me", "Open in-review", "Open ship-it"]);
	});

	it("shows the canonical session reference (@<project>-<num>) on each worker row", () => {
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });
		const ref = screen.getByText("@proj-1-1");
		expect(ref).toBeInTheDocument();
		// Full id on hover via the native title.
		expect(ref).toHaveAttribute("title", "@proj-1-1");
	});

	// Session-row visual hierarchy (decision 2026-07-11): the work name is the
	// prominent headline; the @<project>-<num> id recedes to a muted, non-link
	// subordinate line (reverts the #58 refined-blue in the sidebar only — the
	// whole row is already the click target, so the id must not read as a link).
	describe("session row hierarchy", () => {
		it("renders the work name as the prominent primary line (larger + medium weight)", () => {
			renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });
			const name = screen.getByText("fix login");
			expect(name.className).toContain("font-medium");
			// Larger than the 10.5px id below it, so it reads as the headline.
			expect(name.className).toContain("text-[12.5px]");
			// A real foreground token, not the muted/passive id colour.
			expect(name.className).toMatch(/text-(foreground|muted-foreground)/);
		});

		it("renders the @<project>-<num> id as a muted, non-link subordinate line", () => {
			renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });
			const id = screen.getByText("@proj-1-1");
			// Quiet secondary text — never the bright refined-blue link look (#58).
			expect(id.className).toContain("text-passive");
			expect(id.className).not.toContain("text-accent");
			// Not styled as a link: no underline, no hover colour/underline change.
			expect(id.className).not.toMatch(/underline/);
			expect(id.className).not.toMatch(/hover:/);
			// Monospace still reads well for an id; keep it.
			expect(id.className).toContain("font-mono");
			// Smaller than the name, and it is plain text, not an anchor.
			expect(id.className).toContain("text-[10.5px]");
			expect(id.tagName).toBe("SPAN");
		});

		it("places the Jira key badge on line 2 — above the @<project>-<num> id (enhancement #4)", () => {
			const jiraSession: WorkspaceSession = { ...session, issueId: "jira:PROJ-2272" };
			renderSidebar({ workspaces: [{ ...workspace, sessions: [jiraSession] }] });
			const key = screen.getByText("PROJ-2272");
			const ref = screen.getByText("@proj-1-1");
			// The badge is rendered directly under the work name, so it precedes the
			// id line in DOM order.
			expect(key.compareDocumentPosition(ref) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		});
	});

	// Click-to-copy beside the @<project>-<num> line. The value that reaches the
	// clipboard is the BARE canonical id — the `@` is a prose sigil, while every
	// CLI that takes a session (`ao send --session <id>`, `ao session get <id>`)
	// wants `<project>-<num>`. Copying the sigil would be worse than useless: it
	// would paste into a shell and fail.
	describe("copy session id", () => {
		function copyButton(sessionId = "proj-1-1"): HTMLElement {
			return screen.getByRole("button", { name: `Copy session id ${sessionId}` });
		}

		it("puts the BARE canonical id on the clipboard — no @ sigil", async () => {
			const writeText = vi.fn().mockResolvedValue(undefined);
			window.ao!.clipboard.writeText = writeText;
			renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

			await userEvent.click(copyButton());

			expect(writeText).toHaveBeenCalledWith("proj-1-1");
			// Explicitly not the display form, and not the row's work name.
			expect(writeText).not.toHaveBeenCalledWith("@proj-1-1");
			expect(writeText).not.toHaveBeenCalledWith("fix login");
		});

		// Positive controls for the "does not navigate" assertions below: the row
		// still opens on click, or those assertions would pass vacuously. The second
		// case guards the regression the restructure could introduce — the row's
		// content is no longer inside the open button, so clicking the id text (or
		// the Jira chip) must still reach the row's own handler rather than land in
		// a dead zone.
		it("still opens the session when the row itself is clicked", async () => {
			renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

			await userEvent.click(screen.getByRole("button", { name: "Open fix login" }));

			expect(navigateMock).toHaveBeenCalledWith({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId: "proj-1", sessionId: "proj-1-1" },
			});
		});

		it("still opens the session when the @id text beside the icon is clicked", async () => {
			renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

			await userEvent.click(screen.getByText("@proj-1-1"));

			expect(navigateMock).toHaveBeenCalledWith({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId: "proj-1", sessionId: "proj-1-1" },
			});
		});

		it("does not open or select the row when copying", async () => {
			window.ao!.clipboard.writeText = vi.fn().mockResolvedValue(undefined);
			renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

			await userEvent.click(copyButton());

			expect(navigateMock).not.toHaveBeenCalled();
		});

		it("names itself after what it will copy, so it is not an unlabelled icon", () => {
			renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });
			const button = copyButton();
			expect(button).toHaveAccessibleName("Copy session id proj-1-1");
			// The icon itself carries no accessible text of its own.
			expect(button.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
		});

		it("copies on keyboard activation and stays reachable by Tab", async () => {
			const writeText = vi.fn().mockResolvedValue(undefined);
			window.ao!.clipboard.writeText = writeText;
			renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

			copyButton().focus();
			expect(copyButton()).toHaveFocus();
			await userEvent.keyboard("{Enter}");

			expect(writeText).toHaveBeenCalledWith("proj-1-1");
			expect(navigateMock).not.toHaveBeenCalled();
		});

		it("confirms the copy with a check that reverts to the copy icon", async () => {
			window.ao!.clipboard.writeText = vi.fn().mockResolvedValue(undefined);
			renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

			await userEvent.click(copyButton());
			const copied = await screen.findByRole("button", { name: "Copied session id proj-1-1" });
			// Feedback must not resize the row: the same box, with the glyph swapped.
			expect(copied.className).toContain("size-[13px]");

			await waitFor(() => expect(copyButton()).toBeInTheDocument(), { timeout: 3000 });
		});
	});

	// Breathing status dot (decision 2026-07-11): the lane glyph gently pulses
	// ONLY while the session is working; every other state is static; and the
	// pulse is disabled under prefers-reduced-motion.
	describe("working status dot breathing", () => {
		function glyphOf(title: string): SVGElement | null {
			// The open target is a transparent layer over the row, so the glyph lives
			// beside it in the row's content — it is the row's first svg.
			return screen.getByLabelText(`Open ${title}`).closest("li")?.querySelector("svg") ?? null;
		}

		it("breathes the status glyph while the session is working", () => {
			renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });
			expect(glyphOf("fix login")?.getAttribute("class") ?? "").toContain("animate-status-pulse");
		});

		it("keeps the status glyph static for non-working sessions", () => {
			const merge = { ...session, id: "proj-1-2", title: "ship it", status: "mergeable" as const };
			renderSidebar({ workspaces: [{ ...workspace, sessions: [merge] }] });
			expect(glyphOf("ship it")?.getAttribute("class") ?? "").not.toContain("animate-status-pulse");
		});

		it("keeps the working glyph static under prefers-reduced-motion", () => {
			vi.spyOn(window, "matchMedia").mockImplementation(
				(query: string) =>
					({
						matches: query.includes("prefers-reduced-motion"),
						media: query,
						onchange: null,
						addEventListener: () => undefined,
						removeEventListener: () => undefined,
						addListener: () => undefined,
						removeListener: () => undefined,
						dispatchEvent: () => false,
					}) as unknown as MediaQueryList,
			);
			renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });
			expect(glyphOf("fix login")?.getAttribute("class") ?? "").not.toContain("animate-status-pulse");
		});
	});

	it("confirms project removal before calling the remove handler", async () => {
		const user = userEvent.setup();
		const onRemoveProject = renderSidebar();

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));

		expect(window.confirm).toHaveBeenCalledWith(
			"Remove project Project One? This stops its live sessions and removes it from the sidebar, but keeps the repository folder and stored history on disk.",
		);
		await waitFor(() => expect(onRemoveProject).toHaveBeenCalledTimes(1));
	});

	it("does not remove the project when confirmation is cancelled", async () => {
		vi.mocked(window.confirm).mockReturnValue(false);
		const user = userEvent.setup();
		const onRemoveProject = renderSidebar();

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));

		expect(onRemoveProject).not.toHaveBeenCalled();
	});

	it("reveals dashboard and orchestrator buttons alongside the kebab on the project row", () => {
		renderSidebar();

		expect(screen.getByLabelText("Open Project One dashboard")).toBeInTheDocument();
		expect(screen.getByLabelText("Spawn Project One orchestrator")).toBeInTheDocument();
		expect(screen.getByLabelText("Project actions for Project One")).toBeInTheDocument();
	});

	it("navigates to the project board when the dashboard button is clicked", async () => {
		const user = userEvent.setup();
		renderSidebar();

		await user.click(screen.getByLabelText("Open Project One dashboard"));

		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId", params: { projectId: "proj-1" } });
	});

	// Active-view glow (decision 2026-07-11): exactly one segment across the whole
	// sidebar shows the refined-blue glow — the active project's open view — wired
	// to the real route (useSelection). SEG_ACTIVE_CLASS is marked aria-current.
	describe("active-view glow", () => {
		const glow = "shadow-[0_0_0_1px_var(--accent)";

		function dashboardBtn(name = "Project One") {
			return screen.getByLabelText(`Open ${name} dashboard`);
		}
		function orchestratorBtn(name = "Project One") {
			return screen.getByLabelText(new RegExp(`(Open|Spawn) ${name} orchestrator`));
		}

		it("glows the Dashboard button on the project dashboard route (no session open)", () => {
			mockParams.projectId = "proj-1";
			mockParams.sessionId = undefined;
			renderSidebar({ workspaces: [{ ...workspace, sessions: [orchestratorSession] }] });

			expect(dashboardBtn()).toHaveAttribute("aria-current", "page");
			expect(dashboardBtn().className).toContain(glow);
			expect(dashboardBtn().className).toContain("bg-accent-weak");
			expect(orchestratorBtn()).not.toHaveAttribute("aria-current");
			expect(orchestratorBtn().className).not.toContain(glow);
		});

		it("moves the glow to Orchestrator when its session route is open", () => {
			mockParams.projectId = "proj-1";
			mockParams.sessionId = "proj-1-orchestrator";
			renderSidebar({ workspaces: [{ ...workspace, sessions: [orchestratorSession] }] });

			expect(orchestratorBtn()).toHaveAttribute("aria-current", "page");
			expect(orchestratorBtn().className).toContain(glow);
			expect(dashboardBtn()).not.toHaveAttribute("aria-current");
			expect(dashboardBtn().className).not.toContain(glow);
		});

		it("glows neither button while a worker session route is open", () => {
			mockParams.projectId = "proj-1";
			mockParams.sessionId = "proj-1-1";
			renderSidebar({ workspaces: [{ ...workspace, sessions: [session, orchestratorSession] }] });

			expect(dashboardBtn()).not.toHaveAttribute("aria-current");
			expect(orchestratorBtn()).not.toHaveAttribute("aria-current");
			expect(dashboardBtn().className).not.toContain(glow);
			expect(orchestratorBtn().className).not.toContain(glow);
		});

		it("glows only the active project's button across multiple projects", () => {
			const workspace2: WorkspaceSummary = { ...workspace, id: "proj-2", name: "Project Two", sessions: [] };
			mockParams.projectId = "proj-2";
			mockParams.sessionId = undefined;
			renderSidebar({ workspaces: [{ ...workspace, sessions: [orchestratorSession] }, workspace2] });

			expect(dashboardBtn("Project Two")).toHaveAttribute("aria-current", "page");
			expect(dashboardBtn("Project Two").className).toContain(glow);
			// The other project's dashboard — same view kind, different project — stays dark.
			expect(dashboardBtn("Project One")).not.toHaveAttribute("aria-current");
			expect(dashboardBtn("Project One").className).not.toContain(glow);
			expect(orchestratorBtn("Project One")).not.toHaveAttribute("aria-current");
		});
	});

	// The project header's Orchestrator button carries a busy dot while the
	// project's live orchestrator is working, so "is it working, or is it my
	// turn?" is answerable from the sidebar alone. Busy is the daemon's DERIVED
	// status ("working"), never the raw activity reading — the raw one is stranded
	// forever by a lost Stop hook, and an indicator stuck on busy is worse than no
	// indicator. Everything else, including every flavour of waiting-for-the-human,
	// must fall back to today's exact idle look.
	describe("orchestrator busy indicator", () => {
		function orchestratorBtn(name = "Project One") {
			return screen.getByLabelText(new RegExp(`(Open|Spawn) ${name} orchestrator`));
		}
		function busyDot() {
			return screen.queryByTestId("orchestrator-busy");
		}
		const orch = (status: WorkspaceSession["status"], extra: Partial<WorkspaceSession> = {}): WorkspaceSession => ({
			...orchestratorSession,
			status,
			...extra,
		});
		function mockReducedMotion(reduce: boolean) {
			vi.spyOn(window, "matchMedia").mockImplementation(
				(query: string) =>
					({
						matches: reduce && query.includes("prefers-reduced-motion"),
						media: query,
						onchange: null,
						addEventListener: () => undefined,
						removeEventListener: () => undefined,
						addListener: () => undefined,
						removeListener: () => undefined,
						dispatchEvent: () => false,
					}) as unknown as MediaQueryList,
			);
		}

		it("shows a breathing dot while the orchestrator is working", () => {
			renderSidebar({ workspaces: [{ ...workspace, sessions: [orch("working")] }] });

			const dot = busyDot();
			expect(dot).toBeInTheDocument();
			expect(orchestratorBtn()).toContainElement(dot);
			// Tinted with the status token that already maps 1:1 to the daemon's
			// working status, and breathing with the app's one status animation.
			expect(dot?.className).toContain("bg-working");
			expect(dot?.className).toContain("animate-status-pulse");
		});

		it("leaves the idle button exactly as it is today", () => {
			renderSidebar({ workspaces: [{ ...workspace, sessions: [orch("idle")] }] });

			expect(busyDot()).not.toBeInTheDocument();
			// Nothing but the icon and the label — the dot is not rendered hidden,
			// it is not rendered at all.
			expect(orchestratorBtn().children).toHaveLength(2);
			expect(orchestratorBtn()).toHaveAccessibleName("Open Project One orchestrator");
		});

		it("keeps the button's own classes identical in both states, so nothing reflows", () => {
			renderSidebar({ workspaces: [{ ...workspace, sessions: [orch("idle")] }] });
			const idleClass = orchestratorBtn().className;

			rerenderWorkspaces([{ ...workspace, sessions: [orch("working")] }]);

			// The indicator is out of flow: going busy must not add, drop or swap a
			// single utility on the button, or the label re-wraps/re-truncates and
			// the header jumps every time the orchestrator starts thinking.
			expect(orchestratorBtn().className).toBe(idleClass);
		});

		it("clears the dot when the orchestrator stops working", () => {
			renderSidebar({ workspaces: [{ ...workspace, sessions: [orch("working")] }] });
			expect(busyDot()).toBeInTheDocument();

			rerenderWorkspaces([{ ...workspace, sessions: [orch("idle")] }]);

			expect(busyDot()).not.toBeInTheDocument();
			expect(orchestratorBtn()).toHaveAccessibleName("Open Project One orchestrator");
		});

		// Every non-working state the daemon can report. None of them may leave the
		// indicator stuck on busy — that is the whole correctness bar for this
		// feature. active_stale/idle_aged are the daemon's own timeout guesses that
		// rescue a lost Stop hook, and both mean "your turn", not "working".
		it.each([
			["the agent asked a question", orch("needs_input", { statusReason: "waiting_input" })],
			["a lost Stop hook aged the active reading out", orch("needs_input", { statusReason: "active_stale" })],
			["the turn ended and went quiet", orch("needs_input", { statusReason: "idle_aged" })],
			["no hook has ever reported", orch("no_signal", { statusReason: "no_signal" })],
			["the session is idle", orch("idle", { statusReason: "idle" })],
			["the session was terminated", orch("terminated", { statusReason: "terminated" })],
			["the session is suspended", orch("idle", { isSuspended: true })],
		])("stays idle when %s", (_case, orchestrator) => {
			renderSidebar({ workspaces: [{ ...workspace, sessions: [orchestrator] }] });

			expect(busyDot()).not.toBeInTheDocument();
		});

		it("stays idle when the project has no orchestrator at all", () => {
			renderSidebar({ workspaces: [{ ...workspace, sessions: [] }] });

			expect(busyDot()).not.toBeInTheDocument();
			expect(orchestratorBtn()).toHaveAccessibleName("Spawn Project One orchestrator");
		});

		it("ignores a working orchestrator that belongs to another project", () => {
			const workspace2: WorkspaceSummary = { ...workspace, id: "proj-2", name: "Project Two", sessions: [] };
			renderSidebar({ workspaces: [{ ...workspace, sessions: [orch("working")] }, workspace2] });

			expect(orchestratorBtn("Project One").querySelector("[data-testid='orchestrator-busy']")).toBeInTheDocument();
			expect(orchestratorBtn("Project Two").querySelector("[data-testid='orchestrator-busy']")).toBeNull();
		});

		it("keeps the dot but drops the breathing under prefers-reduced-motion", () => {
			mockReducedMotion(true);
			renderSidebar({ workspaces: [{ ...workspace, sessions: [orch("working")] }] });

			// Presence, not motion, is what separates busy from idle — so the two
			// states stay distinguishable with the animation switched off.
			expect(busyDot()).toBeInTheDocument();
			expect(busyDot()?.className).toContain("bg-working");
			expect(busyDot()?.className).not.toContain("animate-status-pulse");
		});

		it("exposes busy on the button's name and adds no live region", () => {
			mockReducedMotion(false);
			renderSidebar({ workspaces: [{ ...workspace, sessions: [orch("working")] }] });

			expect(orchestratorBtn()).toHaveAccessibleName("Open Project One orchestrator, working");
			// The dot is decorative: the name already carries the state.
			expect(busyDot()).toHaveAttribute("aria-hidden", "true");
			// Deliberately NOT a live region: the sidebar is persistent navigation,
			// and announcing every project's orchestrator on every flip would
			// interrupt the user constantly. The name is read when they ask for it.
			expect(document.querySelectorAll("[aria-live]")).toHaveLength(0);
			expect(orchestratorBtn()).not.toHaveAttribute("aria-busy");
		});
	});

	it("requires explicit worker and orchestrator agents when creating a project", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/new-project");
		renderSidebar({ onCreateProject });

		await user.click(screen.getByLabelText("New project"));
		expect(screen.getByRole("dialog", { name: "Import to Agent Orchestrator" })).toBeInTheDocument();
		expect(window.ao!.app.chooseDirectory).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: /^Project/i }));
		expect(await screen.findByRole("dialog", { name: "Import project" })).toBeInTheDocument();
		expect(window.ao!.app.chooseDirectory).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: /Choose a project folder/i }));

		expect(await screen.findByText("/repo/new-project")).toBeInTheDocument();
		const dialog = screen.getByRole("dialog", { name: "Project agents" });
		expect(dialog).toHaveClass("left-1/2", "top-1/2", "-translate-x-1/2", "-translate-y-1/2");
		await chooseOption(screen.getByRole("combobox", { name: "Worker agent" }), "Codex");
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith({
				path: "/repo/new-project",
				workerAgent: "codex",
				orchestratorAgent: "claude-code",
				asWorkspace: false,
			}),
		);
	});

	it("can create a workspace project from the project add flow", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		renderSidebar({ onCreateProject });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Workspace/i }));
		expect(await screen.findByRole("dialog", { name: "Import workspace" })).toBeInTheDocument();
		expect(window.ao!.app.chooseDirectory).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: /Choose a folder/i }));

		expect(await screen.findByText("/repo/workspace")).toBeInTheDocument();
		expect(screen.getByRole("dialog", { name: "Workspace agents" })).toBeInTheDocument();
		await chooseOption(screen.getByRole("combobox", { name: "Worker agent" }), "Codex");
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create workspace and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith({
				path: "/repo/workspace",
				workerAgent: "codex",
				orchestratorAgent: "claude-code",
				asWorkspace: true,
			}),
		);
	});

	it("shows detected repository validation when workspace import fails", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockRejectedValue(new Error("workspace not registered")) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/Users/test/dev/acme");
		window.ao!.app.scanImportFolder = vi.fn().mockResolvedValue({
			path: "/Users/test/dev/acme",
			repos: [
				{
					name: "web",
					path: "/Users/test/dev/acme/web",
					relativePath: "web",
					branch: "HEAD",
					remote: "",
					hasRemote: false,
					status: "error",
					reason: "Origin remote is required.",
				},
				{
					name: "api",
					path: "/Users/test/dev/acme/api",
					relativePath: "api",
					branch: "main",
					remote: "git@github.com:acme/api.git",
					hasRemote: true,
					status: "ok",
				},
			],
		});
		renderSidebar({ onCreateProject });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Workspace/i }));
		await user.click(await screen.findByRole("button", { name: /Choose a folder/i }));
		await screen.findByRole("dialog", { name: "Workspace agents" });
		await chooseOption(screen.getByRole("combobox", { name: "Worker agent" }), "Codex");
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create workspace and start" }));

		expect(await screen.findByText(/Import failed · workspace not registered/i)).toBeInTheDocument();
		expect(screen.getByText("workspace not registered")).toBeInTheDocument();
		expect(screen.getByText("web")).toBeInTheDocument();
		expect(screen.getByText("Origin remote is required.")).toBeInTheDocument();
		expect(screen.getByText("api")).toBeInTheDocument();
		expect(screen.getByText("main github.com/acme/api")).toBeInTheDocument();
		expect(screen.getByText("Resolve 1 failed repository to continue")).toBeInTheDocument();
		expect(window.ao!.app.scanImportFolder).toHaveBeenCalledWith({
			path: "/Users/test/dev/acme",
			mode: "workspace",
		});
	});

	it("does not rescan folders for non-validation create failures", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockRejectedValue(new Error("AO daemon is not ready.")) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/workspace");
		window.ao!.app.scanImportFolder = vi.fn();
		renderSidebar({ onCreateProject });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Workspace/i }));
		await user.click(await screen.findByRole("button", { name: /Choose a folder/i }));
		await screen.findByRole("dialog", { name: "Workspace agents" });
		await chooseOption(screen.getByRole("combobox", { name: "Worker agent" }), "Codex");
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create workspace and start" }));

		expect(await screen.findByText("AO daemon is not ready.")).toBeInTheDocument();
		expect(window.ao!.app.scanImportFolder).not.toHaveBeenCalled();
	});

	it("opens global settings from the footer menu when no project is selected", async () => {
		const user = userEvent.setup();
		renderSidebar();

		await user.click(screen.getByRole("button", { name: /project actions/i }));

		expect(await screen.findByRole("menuitem", { name: /settings/i })).toBeInTheDocument();
	});

	it("shows needs-auth agents as unavailable while keeping authorized agents selectable", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/new-project");
		getMock.mockResolvedValueOnce({
			data: {
				supported: [
					{ id: "claude-code", label: "Claude Code" },
					{ id: "cursor", label: "Cursor" },
					{ id: "aider", label: "Aider" },
				],
				installed: [
					{ id: "claude-code", label: "Claude Code", authStatus: "authorized" },
					{ id: "cursor", label: "Cursor", authStatus: "unauthorized" },
				],
				authorized: [{ id: "claude-code", label: "Claude Code", authStatus: "authorized" }],
			},
			error: undefined,
		});
		renderSidebar({ onCreateProject, seedAgents: false });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Project/i }));
		await user.click(await screen.findByRole("button", { name: /Choose a project folder/i }));
		expect(await screen.findByText("/repo/new-project")).toBeInTheDocument();

		await user.click(screen.getByRole("combobox", { name: "Worker agent" }));
		const options = await screen.findAllByRole("option");
		expect(options.map((option) => option.textContent)).toEqual([
			"Claude Code",
			"CursorNeeds auth",
			"AiderNeeds install",
		]);
		expect(options[1]).toHaveAttribute("aria-disabled", "true");
		expect(options[2]).toHaveAttribute("aria-disabled", "true");
		await user.keyboard("{Escape}");

		await chooseOption(screen.getByRole("combobox", { name: "Worker agent" }), "Claude Code");
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith(expect.objectContaining({ workerAgent: "claude-code" })),
		);
	});

	it("updates project agent options when the catalog loads after the dialog opens", async () => {
		const user = userEvent.setup();
		const onCreateProject = vi.fn().mockResolvedValue(undefined) as CreateProjectHandler;
		window.ao!.app.chooseDirectory = vi.fn().mockResolvedValue("/repo/new-project");
		let resolveAgents!: (value: {
			data: {
				supported: { id: string; label: string }[];
				installed: { id: string; label: string }[];
				authorized: { id: string; label: string; authStatus: "authorized" }[];
			};
			error: undefined;
		}) => void;
		getMock.mockReturnValueOnce(
			new Promise((resolve) => {
				resolveAgents = resolve;
			}),
		);
		renderSidebar({ onCreateProject, seedAgents: false });

		await user.click(screen.getByLabelText("New project"));
		await user.click(screen.getByRole("button", { name: /^Project/i }));
		await user.click(await screen.findByRole("button", { name: /Choose a project folder/i }));
		expect(await screen.findByText("/repo/new-project")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Create and start" })).toBeDisabled();

		resolveAgents({
			data: {
				supported: [
					{ id: "claude-code", label: "Claude Code" },
					{ id: "codex", label: "Codex" },
				],
				installed: [
					{ id: "claude-code", label: "Claude Code" },
					{ id: "codex", label: "Codex" },
				],
				authorized: [
					{ id: "claude-code", label: "Claude Code", authStatus: "authorized" },
					{ id: "codex", label: "Codex", authStatus: "authorized" },
				],
			},
			error: undefined,
		});

		await chooseOption(screen.getByRole("combobox", { name: "Worker agent" }), "Codex");
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "Claude Code");
		await user.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() =>
			expect(onCreateProject).toHaveBeenCalledWith({
				path: "/repo/new-project",
				workerAgent: "codex",
				orchestratorAgent: "claude-code",
				trackerIntake: undefined,
				asWorkspace: false,
			}),
		);
	});

	it("renames a session inline and persists via the daemon", async () => {
		const user = userEvent.setup();
		const workspaceWithSession = { ...workspace, sessions: [session] };
		renderSidebar({ workspaces: [workspaceWithSession] });

		await user.click(screen.getByLabelText("Rename fix login"));
		const input = screen.getByLabelText("Rename fix login");
		await user.clear(input);
		await user.type(input, "polish login{Enter}");

		await waitFor(() => expect(renameSessionMock).toHaveBeenCalledWith("proj-1-1", "polish login"));
	});

	it("caps the inline rename input at 20 characters", async () => {
		const user = userEvent.setup();
		const workspaceWithSession = { ...workspace, sessions: [session] };
		renderSidebar({ workspaces: [workspaceWithSession] });

		await user.click(screen.getByLabelText("Rename fix login"));
		expect(screen.getByLabelText("Rename fix login")).toHaveAttribute("maxlength", "20");
	});

	it("cancels the inline rename on Escape without calling the daemon", async () => {
		const user = userEvent.setup();
		const workspaceWithSession = { ...workspace, sessions: [session] };
		renderSidebar({ workspaces: [workspaceWithSession] });

		await user.click(screen.getByLabelText("Rename fix login"));
		const input = screen.getByLabelText("Rename fix login");
		await user.clear(input);
		await user.type(input, "discard me{Escape}");

		expect(renameSessionMock).not.toHaveBeenCalled();
		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
	});

	function projectHeading(name = "Project One"): HTMLButtonElement {
		const heading = screen.getByText(name).closest("button");
		if (!heading) throw new Error("Project heading button not found");
		return heading as HTMLButtonElement;
	}

	it("collapses the section on heading click, hiding the action buttons and sessions", async () => {
		const user = userEvent.setup();
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		// Expanded by default: labeled buttons + session row visible.
		expect(screen.getByLabelText("Open Project One dashboard")).toBeInTheDocument();
		expect(screen.getByLabelText("Open fix login")).toBeInTheDocument();
		const heading = projectHeading();
		expect(heading).toHaveAttribute("aria-expanded", "true");

		await user.click(heading);

		expect(heading).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByLabelText("Open Project One dashboard")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Spawn Project One orchestrator")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();

		// Clicking the heading toggles; it does not navigate.
		expect(navigateMock).not.toHaveBeenCalled();

		await user.click(heading);
		expect(heading).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByLabelText("Open Project One dashboard")).toBeInTheDocument();
	});

	it("persists collapse to the ui-store when a project is toggled", async () => {
		const user = userEvent.setup();
		renderSidebar();

		await user.click(projectHeading());

		expect(useUiStore.getState().collapsedProjectIds.has("proj-1")).toBe(true);
	});

	it("renders a project collapsed when the ui-store marks it collapsed", () => {
		useUiStore.setState({ collapsedProjectIds: new Set(["proj-1"]) });
		renderSidebar({ workspaces: [{ ...workspace, sessions: [session] }] });

		expect(projectHeading()).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByLabelText("Open Project One dashboard")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Open fix login")).not.toBeInTheDocument();
	});

	it("does not collapse the project when the overflow menu is opened", async () => {
		const user = userEvent.setup();
		renderSidebar();
		const heading = projectHeading();
		expect(heading).toHaveAttribute("aria-expanded", "true");

		await user.click(screen.getByLabelText("Project actions for Project One"));

		// The overflow trigger opens its menu without toggling the section collapse.
		expect(await screen.findByRole("menuitem", { name: "Project settings" })).toBeInTheDocument();
		expect(heading).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByLabelText("Open Project One dashboard")).toBeInTheDocument();
	});
});

describe("Sidebar project reorder (drag and drop)", () => {
	const three: WorkspaceSummary[] = [
		{ id: "proj-a", name: "Alpha", path: "/repo/a", sessions: [] },
		{ id: "proj-b", name: "Beta", path: "/repo/b", sessions: [] },
		{ id: "proj-c", name: "Gamma", path: "/repo/c", sessions: [] },
	];

	// jsdom has no drag pipeline and getBoundingClientRect is all-zeros, so a
	// positive clientY lands past the (zero) midpoint → "bottom" edge.
	function dataTransfer() {
		return { effectAllowed: "", dropEffect: "", setData: vi.fn(), getData: vi.fn() };
	}
	function cardBox(name: string): HTMLElement {
		const box = screen.getByText(name).closest("[draggable='true']");
		if (!box) throw new Error(`draggable card for ${name} not found`);
		return box as HTMLElement;
	}
	function dropZone(name: string): HTMLElement {
		const li = screen.getByText(name).closest("li");
		if (!li) throw new Error(`drop zone for ${name} not found`);
		return li as HTMLElement;
	}

	it("commits and persists a new order when a card is dropped past another", () => {
		renderSidebar({ workspaces: three });
		const dt = dataTransfer();

		fireEvent.dragStart(cardBox("Alpha"), { dataTransfer: dt });
		fireEvent.dragOver(dropZone("Gamma"), { dataTransfer: dt, clientY: 500 });
		fireEvent.drop(dropZone("Gamma"), { dataTransfer: dt });

		// Alpha dropped below Gamma → [Beta, Gamma, Alpha], persisted to the store.
		expect(useUiStore.getState().projectOrder).toEqual(["proj-b", "proj-c", "proj-a"]);
		expect(JSON.parse(localStorage.getItem("ao.projects.order")!)).toEqual(["proj-b", "proj-c", "proj-a"]);
	});

	it("does not reorder when the drag starts from the overflow menu (data-no-drag)", () => {
		renderSidebar({ workspaces: three });
		const dt = dataTransfer();

		// Dragging from the ⋮ control must be cancelled, so no order is committed.
		fireEvent.dragStart(screen.getByLabelText("Project actions for Alpha"), { dataTransfer: dt });
		fireEvent.dragOver(dropZone("Gamma"), { dataTransfer: dt, clientY: 500 });
		fireEvent.drop(dropZone("Gamma"), { dataTransfer: dt });

		expect(useUiStore.getState().projectOrder).toEqual([]);
		expect(localStorage.getItem("ao.projects.order")).toBeNull();
	});

	it("does not make cards draggable when there is only one project", () => {
		renderSidebar({ workspaces: [three[0]] });
		expect(screen.getByText("Alpha").closest("[draggable='true']")).toBeNull();
	});

	// The orchestrator is the ONE session with no board card and no sidebar row of
	// its own (workerSessions() filters it out of both surfaces that render
	// IdleStatusChip), so without a chip on the project header's Orchestrator
	// button the idle-suspend countdown the daemon already publishes for it would
	// be invisible — and the human would meet the pause as a surprise rather than
	// see it coming. The resting look must stay exactly as it is today.
	describe("orchestrator idle-close countdown", () => {
		function orchestratorBtn(name = "Project One") {
			return screen.getByLabelText(new RegExp(`(Open|Spawn) ${name} orchestrator`));
		}
		const IN_WINDOW = new Date(Date.now() + 30 * 60 * 1000).toISOString(); // 30m out
		const FAR_OUT = new Date(Date.now() + 70 * 60 * 60 * 1000).toISOString(); // 70h out
		const orch = (extra: Partial<WorkspaceSession> = {}): WorkspaceSession => ({
			...orchestratorSession,
			status: "idle",
			...extra,
		});

		it("counts down on the Orchestrator button as the deadline nears", () => {
			renderSidebar({ workspaces: [{ ...workspace, sessions: [orch({ idleCloseAt: IN_WINDOW })] }] });

			// The remaining time is rendered inside the button the human can see
			// without opening the session.
			expect(orchestratorBtn()).toHaveTextContent(/\d+[hm]/);
			// ...and rides the accessible name, because an explicit aria-label on the
			// button hides the chip's own text from assistive tech.
			expect(orchestratorBtn()).toHaveAccessibleName(/auto-suspends in/);
		});

		it("says paused once the orchestrator has been suspended", () => {
			renderSidebar({
				workspaces: [{ ...workspace, sessions: [orch({ isSuspended: true, idleCloseAt: IN_WINDOW })] }],
			});

			expect(screen.getByLabelText("Paused")).toBeInTheDocument();
			// Paused wins over the countdown: there is no deadline left to count to.
			expect(orchestratorBtn()).toHaveAccessibleName("Open Project One orchestrator, paused");
		});

		it("leaves the button untouched while the deadline is far out", () => {
			renderSidebar({ workspaces: [{ ...workspace, sessions: [orch({ idleCloseAt: FAR_OUT })] }] });

			// Same bar the busy dot holds: a session nowhere near expiry looks
			// exactly like today — icon and label only, nothing else rendered.
			expect(orchestratorBtn().children).toHaveLength(2);
			expect(orchestratorBtn()).toHaveAccessibleName("Open Project One orchestrator");
		});

		it("renders nothing when the project has no orchestrator", () => {
			renderSidebar({ workspaces: [{ ...workspace, sessions: [] }] });

			expect(orchestratorBtn()).toHaveAccessibleName("Spawn Project One orchestrator");
		});
	});
});
