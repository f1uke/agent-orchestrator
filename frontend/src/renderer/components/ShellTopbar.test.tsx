import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionActivityState, WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { ShellTopbar } from "./ShellTopbar";

const { navigateMock, paramsMock, pathnameMock, useWorkspaceQueryMock } = vi.hoisted(() => ({
	navigateMock: vi.fn(),
	paramsMock: { projectId: undefined as string | undefined, sessionId: undefined as string | undefined },
	pathnameMock: { value: "/" },
	useWorkspaceQueryMock: vi.fn(),
}));

vi.mock("@tanstack/react-router", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-router")>();
	return {
		...actual,
		useNavigate: () => navigateMock,
		useParams: () => paramsMock,
		useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => unknown }) =>
			select({ location: { pathname: pathnameMock.value } }),
	};
});

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => useWorkspaceQueryMock(),
	workspaceQueryKey: ["workspaces"],
}));

vi.mock("./NewTaskDialog", () => ({ NewTaskDialog: () => null }));
vi.mock("./NotificationCenter", () => ({ NotificationCenter: () => null }));

const worker: WorkspaceSession = {
	id: "sess-1",
	workspaceId: "proj-1",
	workspaceName: "my-app",
	title: "do the thing",
	provider: "claude-code",
	kind: "worker",
	branch: "ao/sess-1",
	status: "working",
	updatedAt: "2026-06-10T00:00:00Z",
	prs: [],
};

function sessionWith(overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		...worker,
		activity: { state: "active", lastActivityAt: "2026-06-10T00:00:00Z" },
		...overrides,
	};
}

function renderTopbar(session: WorkspaceSession, project: Partial<WorkspaceSummary> = {}) {
	const data: WorkspaceSummary[] = [
		{
			id: session.workspaceId,
			name: session.workspaceName,
			path: "/repo/my-app",
			sessions: [session],
			...project,
		},
	];
	useWorkspaceQueryMock.mockReturnValue({ data, isError: false, isLoading: false });
	paramsMock.projectId = session.workspaceId;
	paramsMock.sessionId = session.id;
	pathnameMock.value = `/projects/${session.workspaceId}/sessions/${session.id}`;
	return render(
		<QueryClientProvider client={new QueryClient()}>
			<ShellTopbar />
		</QueryClientProvider>,
	);
}

// Renders the topbar as it appears on a project surface (board or Browse Jira):
// a project in scope, no session selected.
function renderProjectSurface(pathname: string) {
	useWorkspaceQueryMock.mockReturnValue({
		data: [{ id: "proj-1", name: "my-app", path: "/repo/my-app", sessions: [] }] as WorkspaceSummary[],
		isError: false,
		isLoading: false,
	});
	paramsMock.projectId = "proj-1";
	paramsMock.sessionId = undefined;
	pathnameMock.value = pathname;
	return render(
		<QueryClientProvider client={new QueryClient()}>
			<ShellTopbar />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	navigateMock.mockReset();
	paramsMock.projectId = undefined;
	paramsMock.sessionId = undefined;
	pathnameMock.value = "/";
	useWorkspaceQueryMock.mockReset();
	useWorkspaceQueryMock.mockReturnValue({ data: [], isError: false, isLoading: false });
});

describe("ShellTopbar status pill", () => {
	it.each([
		["active", "Working"],
		["idle", "Idle"],
		["waiting_input", "Input Needed"],
		["exited", "Exited"],
	] as const)("renders %s activity as %s", (state: SessionActivityState, label) => {
		renderTopbar(sessionWith({ activity: { state, lastActivityAt: "2026-06-10T00:00:00Z" } }));

		expect(screen.getByText(label)).toBeInTheDocument();
	});

	it.each([
		["ci_failed", "ci_failed", "idle", "Idle", "CI failed"],
		["mergeable", "mergeable", "active", "Working", "Ready"],
		["merged", "done", "exited", "Exited", "Done"],
		["changes_requested", "needs_you", "waiting_input", "Input Needed", "Needs input"],
	] as const)(
		"ignores coarse %s/%s topbar status in favor of activity",
		(status, displayStatus, state, label, hidden) => {
			renderTopbar(
				sessionWith({
					status,
					displayStatus,
					activity: { state, lastActivityAt: "2026-06-10T00:00:00Z" },
				}),
			);

			expect(screen.getByText(label)).toBeInTheDocument();
			expect(screen.queryByText(hidden)).not.toBeInTheDocument();
		},
	);

	it("uses a compact unknown state when activity is missing or unknown", () => {
		const first = renderTopbar(sessionWith({ activity: undefined }));
		expect(screen.getByText("Unknown")).toBeInTheDocument();

		first.unmount();
		renderTopbar(sessionWith({ activity: { state: "unknown", lastActivityAt: "" } }));
		expect(screen.getByText("Unknown")).toBeInTheDocument();
	});
});

describe("ShellTopbar Browse Jira entry", () => {
	it("shows the Browse Jira button on a project board and navigates to the surface", async () => {
		renderProjectSurface("/projects/proj-1");

		const button = screen.getByRole("button", { name: "Browse Jira" });
		expect(button).toBeInTheDocument();
		expect(button).not.toHaveClass("is-active");

		await userEvent.click(button);
		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId/jira", params: { projectId: "proj-1" } });
	});

	it("marks the Browse Jira button active on the Browse Jira surface", () => {
		renderProjectSurface("/projects/proj-1/jira");

		expect(screen.getByRole("button", { name: "Browse Jira" })).toHaveClass("is-active");
	});
});

describe("ShellTopbar worker session actions", () => {
	// Kill moved to the terminal toolbar (KillSessionButton) and the Orchestrator
	// link is redundant with the sidebar's per-project button — the worker header
	// keeps only the inspector toggle now.
	it("shows only the inspector toggle for a worker session (no Kill, no Orchestrator link)", () => {
		renderTopbar(sessionWith());

		expect(screen.getByRole("button", { name: /inspector panel/i })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Kill session" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Open orchestrator" })).not.toBeInTheDocument();
	});
});

describe("ShellTopbar — the member switcher, and where it has to live", () => {
	// The switcher is drawn HERE, not in the terminal toolbar, and the reason is
	// cardinality: `CenterPane` draws its toolbar once per PANE, so a switcher put
	// there appears twice the moment the split view is used. This header is
	// rendered once by the shell for the whole session route. The other half of
	// that proof - that the center pane draws none, however many panes there are -
	// lives in SessionView.test.tsx.
	function renderCrew(activeIsQa: boolean) {
		const dev = sessionWith({ id: "dev-1", crew: { id: "dev-1", role: "dev", hasRun: true } });
		const qa = sessionWith({ id: "dev-1-qa", crew: { id: "dev-1", role: "qa", hasRun: true } });
		useWorkspaceQueryMock.mockReturnValue({
			data: [{ id: "proj-1", name: "my-app", path: "/repo/my-app", sessions: [dev, qa] }] as WorkspaceSummary[],
			isError: false,
			isLoading: false,
		});
		paramsMock.projectId = "proj-1";
		paramsMock.sessionId = activeIsQa ? qa.id : dev.id;
		pathnameMock.value = `/projects/proj-1/sessions/${paramsMock.sessionId}`;
		return render(
			<QueryClientProvider client={new QueryClient()}>
				<ShellTopbar />
			</QueryClientProvider>,
		);
	}

	it("draws EXACTLY ONE switcher on a crew session, with a chip per member", () => {
		renderCrew(false);

		expect(document.querySelectorAll("[data-crew-switcher]")).toHaveLength(1);
		expect(document.querySelectorAll("[data-crew-switcher-chip]")).toHaveLength(2);
	});

	it("marks the routed member, so switching is visible without reading the branch", () => {
		renderCrew(true);

		expect(document.querySelector("[data-crew-switcher-chip='qa']")).toHaveAttribute(
			"data-crew-switcher-chip-active",
			"true",
		);
		expect(document.querySelector("[data-crew-switcher-chip='dev']")).toHaveAttribute(
			"data-crew-switcher-chip-active",
			"false",
		);
	});

	it("navigates to the other member when its chip is pressed", async () => {
		renderCrew(false);

		await userEvent.click(document.querySelector("[data-crew-switcher-chip='qa']") as HTMLElement);

		expect(navigateMock).toHaveBeenCalledWith(
			expect.objectContaining({ params: { projectId: "proj-1", sessionId: "dev-1-qa" } }),
		);
	});

	it("costs a SOLO session one quiet `+ qa` and nothing else", () => {
		renderTopbar(sessionWith());

		expect(document.querySelector("[data-crew-switcher]")).toHaveAttribute("data-crew-switcher", "solo");
		expect(document.querySelector("[data-crew-switcher-add='qa']")).not.toBeNull();
		// No chips and no gate: the identity slot still reads branch + status pill.
		expect(document.querySelectorAll("[data-crew-switcher-chip]")).toHaveLength(0);
		expect(document.querySelector("[data-crew-switcher-gate='review']")).toBeNull();
	});

	it("tells the switcher when this project never forms a crew by itself", () => {
		// The topbar is the only place a human is looking when they wonder where the
		// qa is, and the project fact lives on the workspace summary. Dropping it
		// here would leave `+ qa` promising an automatic crewmate that never comes.
		renderTopbar(sessionWith(), { disableAutoCrew: true });

		expect(document.querySelector("[data-crew-switcher-add='qa']")).toHaveAttribute(
			"data-crew-switcher-add-auto",
			"off",
		);
	});

	it("leaves the switcher's default promise alone on an ordinary project", () => {
		renderTopbar(sessionWith());

		expect(document.querySelector("[data-crew-switcher-add='qa']")).toHaveAttribute(
			"data-crew-switcher-add-auto",
			"on",
		);
	});

	it("draws no switcher at all on an orchestrator session — a crew there is a category error", () => {
		renderTopbar(sessionWith({ kind: "orchestrator" }));

		expect(document.querySelector("[data-crew-switcher]")).toBeNull();
	});
});
