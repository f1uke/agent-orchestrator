import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { searchMock, treeMock, myselfMock, navigateMock, orchMock, sendMock } = vi.hoisted(() => ({
	searchMock: vi.fn(),
	treeMock: vi.fn(),
	myselfMock: vi.fn(),
	navigateMock: vi.fn(),
	orchMock: vi.fn(),
	sendMock: vi.fn(),
}));

vi.mock("../hooks/useSessionJiraContext", () => ({
	useJiraSearch: searchMock,
	useJiraTreeContext: treeMock,
	useJiraMyself: myselfMock,
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: [] }),
	workspaceQueryKey: ["workspaces"],
}));
vi.mock("../types/workspace", () => ({ findProjectOrchestrator: orchMock }));
vi.mock("../lib/api-client", () => ({
	apiClient: { POST: sendMock },
	apiErrorMessage: (_e: unknown, fallback: string) => fallback,
}));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigateMock }));

// Stub the project picker so the test drives project selection directly.
vi.mock("./JiraProjectPicker", () => ({
	JiraProjectPicker: ({ value, onSelect }: { value: { key?: string } | null; onSelect: (p: unknown) => void }) => (
		<button type="button" onClick={() => onSelect({ key: "DEMO", name: "Demo Project" })}>
			picker:{value?.key ?? "none"}
		</button>
	),
}));

// Stub the New-task modal so we can assert the Create-session handoff without its internals.
vi.mock("./NewTaskDialog", () => ({
	NewTaskDialog: ({ open, initialIssue }: { open: boolean; initialIssue?: { key?: string } | null }) =>
		open ? <div data-testid="new-task">dialog:{initialIssue?.key}</div> : null,
}));

// Stub the read-only detail drawer: record which issue it opened for, and expose a
// button that fires its Create-session handoff.
vi.mock("./JiraIssueDetail", () => ({
	JiraIssueDetail: ({
		issueKey,
		open,
		onCreateSession,
	}: {
		issueKey: string | null;
		open: boolean;
		onCreateSession: (issue: { key: string }) => void;
	}) =>
		open ? (
			<div data-testid="jira-detail">
				detail:{issueKey}
				<button type="button" onClick={() => onCreateSession({ key: issueKey! })}>
					detail-create
				</button>
			</div>
		) : null,
}));

import { BrowseJiraPage } from "./BrowseJiraPage";

type Row = {
	key: string;
	type: string;
	title: string;
	status: string;
	statusCategory: string;
	assignee: string;
	assigneeAccountId?: string;
	parent?: { key: string; title?: string };
	sprint?: { name: string; state: string };
};

// Mirror the server-side JQL filtering (assignee accountId / "unassigned" + types)
// for the results fetch; the base fetch (no assignee/types) returns everything.
function applyServerFilters(data: Row[], opts?: { assignee?: string; types?: string[]; jql?: string }): Row[] {
	if (!opts || opts.jql) return data;
	let out = data;
	const types = (opts.types ?? []).map((t) => t.toLowerCase());
	if (types.length > 0) {
		out = out.filter((it) => {
			const t = it.type.toLowerCase();
			return types.some((name) => t.includes(name) || name.includes(t));
		});
	}
	const assignee = opts.assignee ?? "";
	if (assignee === "unassigned") out = out.filter((it) => !it.assignee.trim());
	else if (assignee) out = out.filter((it) => it.assigneeAccountId === assignee);
	return out;
}

// The tree-context hook, computed from the FULL set: the ancestors + descendants of
// the roots (excluding the roots) — what collectTreeContext fetches live.
function computeTreeContext(roots: Row[], all: Row[]): Row[] {
	const seen = new Set(roots.map((r) => r.key));
	const out: Row[] = [];
	let frontier = new Set(roots.map((r) => r.key));
	for (let step = 0; step < 2 && frontier.size > 0; step += 1) {
		const next = new Set<string>();
		for (const r of all) {
			if (r.parent?.key && frontier.has(r.parent.key) && !seen.has(r.key)) {
				seen.add(r.key);
				out.push(r);
				next.add(r.key);
			}
		}
		frontier = next;
	}
	let pending = [...roots, ...out];
	for (let step = 0; step < 2; step += 1) {
		const wanted = new Set<string>();
		for (const r of pending) if (r.parent?.key && !seen.has(r.parent.key)) wanted.add(r.parent.key);
		if (wanted.size === 0) break;
		const found = all.filter((r) => wanted.has(r.key) && !seen.has(r.key));
		found.forEach((r) => seen.add(r.key));
		out.push(...found);
		pending = found;
	}
	return out;
}

let currentData: Row[] | undefined;
function setSearch(over: { data?: Row[] | undefined; isFetching?: boolean; isError?: boolean; error?: unknown } = {}) {
	const hasData = "data" in over;
	currentData = hasData
		? over.data
		: [
				{
					key: "DEMO-101",
					type: "Story",
					title: "Story one",
					status: "Ready for QA",
					statusCategory: "new",
					assignee: "Alex",
					assigneeAccountId: "acc-alex",
				},
				{ key: "DEMO-88", type: "Bug", title: "Bug two", status: "To Do", statusCategory: "new", assignee: "" },
			];
	searchMock.mockImplementation(
		(
			_query: string,
			_project: string,
			_enabled: boolean,
			opts?: { assignee?: string; types?: string[]; jql?: string },
		) => ({
			data: currentData === undefined ? undefined : applyServerFilters(currentData, opts),
			isFetching: over.isFetching ?? false,
			isError: over.isError ?? false,
			error: over.error ?? null,
		}),
	);
	treeMock.mockImplementation((roots: Row[], opts?: { enabled?: boolean }) => ({
		data: currentData && opts?.enabled ? computeTreeContext(roots, currentData) : [],
		isFetching: false,
		isError: false,
		error: null,
	}));
}

// A richer set spanning two sprints + a no-sprint issue, for the grouping / assignee tests.
const richData: Row[] = [
	{
		key: "DEMO-1",
		type: "Story",
		title: "S one",
		status: "To Do",
		statusCategory: "new",
		assignee: "Alex",
		assigneeAccountId: "acc-alex",
		sprint: { name: "Sprint 2026-14", state: "active" },
	},
	{
		key: "DEMO-4",
		type: "Story",
		title: "S four",
		status: "To Do",
		statusCategory: "new",
		assignee: "Sam",
		assigneeAccountId: "acc-sam",
		sprint: { name: "Sprint 2026-14", state: "active" },
	},
	{
		key: "DEMO-2",
		type: "Story",
		title: "S two",
		status: "To Do",
		statusCategory: "new",
		assignee: "Sam",
		assigneeAccountId: "acc-sam",
		sprint: { name: "Sprint 2026-15", state: "future" },
	},
	{ key: "DEMO-3", type: "Bug", title: "B three", status: "To Do", statusCategory: "new", assignee: "" },
];

function renderPage() {
	render(
		<QueryClientProvider client={new QueryClient()}>
			<BrowseJiraPage projectId="proj-1" />
		</QueryClientProvider>,
	);
}

describe("BrowseJiraPage", () => {
	beforeEach(() => {
		searchMock.mockReset();
		treeMock.mockReset();
		myselfMock.mockReset();
		orchMock.mockReset();
		sendMock.mockReset();
		myselfMock.mockReturnValue({ data: { accountId: "" } });
		orchMock.mockReturnValue(undefined); // no orchestrator by default
		sendMock.mockResolvedValue({ error: null });
		setSearch();
		navigateMock.mockReset();
		window.localStorage.clear();
	});

	it("prompts to pick a project before any issues show", () => {
		renderPage();
		expect(screen.getByText(/Pick a project to browse its issues/i)).toBeTruthy();
		expect(screen.queryByText("DEMO-101")).toBeNull();
	});

	it("lists a project's issues once picked and remembers the pick", () => {
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		expect(screen.getByText("DEMO-101")).toBeTruthy();
		expect(screen.getByText("Bug two")).toBeTruthy();
		expect(window.localStorage.getItem("ao.jira.lastProject")).toContain("DEMO");
	});

	it("filters the list by issue type via the chips", () => {
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.click(screen.getByRole("button", { name: "Bug" }));
		expect(screen.queryByText("DEMO-101")).toBeNull();
		expect(screen.getByText("DEMO-88")).toBeTruthy();
		const sawBugType = searchMock.mock.calls.some((call: unknown[]) =>
			(call[3] as { types?: string[] } | undefined)?.types?.includes("Bug"),
		);
		expect(sawBugType).toBe(true);
	});

	it("opens the New-task modal pre-filled when the + action is clicked", () => {
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.click(screen.getByRole("button", { name: "Create a session for DEMO-101" }));
		expect(screen.getByTestId("new-task")).toHaveTextContent("dialog:DEMO-101");
	});

	it("opens the read-only detail drawer when a row is clicked", () => {
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.click(screen.getByRole("button", { name: "Open DEMO-101" }));
		expect(screen.getByTestId("jira-detail")).toHaveTextContent("detail:DEMO-101");
		expect(screen.queryByTestId("new-task")).toBeNull();
	});

	it("the + action does not open the detail drawer", () => {
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.click(screen.getByRole("button", { name: "Create a session for DEMO-101" }));
		expect(screen.getByTestId("new-task")).toHaveTextContent("dialog:DEMO-101");
		expect(screen.queryByTestId("jira-detail")).toBeNull();
	});

	it("Create session from inside the detail drawer hands off to the New-task modal", () => {
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.click(screen.getByRole("button", { name: "Open DEMO-101" }));
		fireEvent.click(screen.getByRole("button", { name: "detail-create" }));
		expect(screen.queryByTestId("jira-detail")).toBeNull();
		expect(screen.getByTestId("new-task")).toHaveTextContent("dialog:DEMO-101");
	});

	it("surfaces a search failure inline", () => {
		setSearch({ data: undefined, isError: true, error: new Error("set JIRA_API_TOKEN") });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));
		expect(screen.getByText(/set JIRA_API_TOKEN/i)).toBeTruthy();
	});

	it("groups issues into collapsible sprint sections by default, with counts", () => {
		setSearch({ data: richData });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		expect(screen.getByText("Sprint 2026-14")).toBeTruthy();
		expect(screen.getByText("Sprint 2026-15")).toBeTruthy();
		expect(screen.getByText("No sprint")).toBeTruthy();
		expect(screen.getByText(/2 work items/)).toBeTruthy();
		expect(screen.getByText("active")).toBeTruthy();

		fireEvent.click(screen.getByRole("button", { name: /Sprint 2026-14/ }));
		expect(screen.queryByText("DEMO-1")).toBeNull();
		expect(screen.getByText("Sprint 2026-14")).toBeTruthy();
	});

	it("toggles grouping off to a flat list and remembers it", () => {
		setSearch({ data: richData });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.click(screen.getByRole("button", { name: "Group by sprint" }));
		expect(screen.queryByText("Sprint 2026-14")).toBeNull();
		expect(screen.getByText("DEMO-1")).toBeTruthy();
		expect(JSON.parse(window.localStorage.getItem("ao.jira.browsePrefs")!).groupBySprint).toBe(false);
	});

	it("filters the list by assignee", () => {
		setSearch({ data: richData });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.change(screen.getByLabelText("Filter by assignee"), { target: { value: "Sam" } });
		expect(screen.getByText("DEMO-4")).toBeTruthy();
		expect(screen.getByText("DEMO-2")).toBeTruthy();
		expect(screen.queryByText("DEMO-1")).toBeNull();
		expect(screen.queryByText("DEMO-3")).toBeNull();
		expect(JSON.parse(window.localStorage.getItem("ao.jira.browsePrefs")!).assignee).toBe("Sam");
		const sawSamAccountId = searchMock.mock.calls.some(
			(call: unknown[]) => (call[3] as { assignee?: string } | undefined)?.assignee === "acc-sam",
		);
		expect(sawSamAccountId).toBe(true);
	});

	it("restores remembered grouping + assignee on return", () => {
		window.localStorage.setItem("ao.jira.browsePrefs", JSON.stringify({ groupBySprint: false, assignee: "Alex" }));
		setSearch({ data: richData });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		expect(screen.queryByText("Sprint 2026-14")).toBeNull();
		expect(screen.getByText("DEMO-1")).toBeTruthy();
		expect(screen.queryByText("DEMO-4")).toBeNull();
	});

	it("pushes hide-done + active-sprint toggles into the server-side query and remembers them", () => {
		setSearch({ data: richData });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.click(screen.getByRole("button", { name: "Hide done" }));
		fireEvent.click(screen.getByRole("button", { name: "Active sprint" }));

		const sawHideDone = searchMock.mock.calls.some(
			(c: unknown[]) => (c[3] as { hideDone?: boolean } | undefined)?.hideDone === true,
		);
		const sawActiveSprint = searchMock.mock.calls.some(
			(c: unknown[]) => (c[3] as { activeSprint?: boolean } | undefined)?.activeSprint === true,
		);
		expect(sawHideDone).toBe(true);
		expect(sawActiveSprint).toBe(true);
		const prefs = JSON.parse(window.localStorage.getItem("ao.jira.browsePrefs")!);
		expect(prefs.hideDone).toBe(true);
		expect(prefs.activeSprintOnly).toBe(true);
	});

	it("advanced JQL mode hides the structured filters and drives the search with the raw query", () => {
		setSearch({ data: richData });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.click(screen.getByRole("button", { name: "Advanced JQL" }));
		expect(screen.queryByRole("button", { name: "Bug" })).toBeNull();
		expect(screen.queryByLabelText("Filter by assignee")).toBeNull();
		fireEvent.change(screen.getByLabelText("Advanced JQL query"), {
			target: { value: "project = STAR AND labels = urgent" },
		});

		const sawJql = searchMock.mock.calls.some(
			(c: unknown[]) => (c[3] as { jql?: string } | undefined)?.jql === "project = STAR AND labels = urgent",
		);
		expect(sawJql).toBe(true);

		fireEvent.click(screen.getByRole("button", { name: /Back to filters/ }));
		expect(screen.getByLabelText("Filter by assignee")).toBeTruthy();
	});

	// ── Fix 2: subtask descent + tree nesting + collapse ──────────────────────────

	it("nests a matched card's own (unmatched) subtasks in the list, dimmed as context", () => {
		// Only the parent Story matches the type filter; its subtask is pulled in via
		// the tree-context descent and shown nested + dimmed.
		const data: Row[] = [
			{ key: "DEMO-1", type: "Story", title: "Parent story", status: "To Do", statusCategory: "new", assignee: "Alex" },
			{
				key: "DEMO-2",
				type: "Sub-task",
				title: "Child task",
				status: "To Do",
				statusCategory: "new",
				assignee: "Sam",
				parent: { key: "DEMO-1", title: "Parent story" },
			},
		];
		setSearch({ data: [data[0]] }); // results = only the Story
		treeMock.mockImplementation((roots: Row[], opts?: { enabled?: boolean }) => ({
			data: opts?.enabled ? computeTreeContext(roots, data) : [],
			isFetching: false,
			isError: false,
			error: null,
		}));
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		expect(screen.getByText("DEMO-1")).toBeTruthy();
		// The subtask, though it didn't match, is fetched + shown as context.
		expect(screen.getByText("DEMO-2")).toBeTruthy();
		expect(screen.getByText("context")).toBeTruthy();
	});

	it("collapses a node's subtree via its chevron and persists it", () => {
		const data: Row[] = [
			{ key: "DEMO-1", type: "Story", title: "Parent story", status: "To Do", statusCategory: "new", assignee: "Alex" },
			{
				key: "DEMO-2",
				type: "Sub-task",
				title: "Child task",
				status: "To Do",
				statusCategory: "new",
				assignee: "Sam",
				parent: { key: "DEMO-1", title: "Parent story" },
			},
		];
		setSearch({ data });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		expect(screen.getByText("DEMO-2")).toBeTruthy();
		fireEvent.click(screen.getByRole("button", { name: "Collapse DEMO-1" }));
		expect(screen.queryByText("DEMO-2")).toBeNull(); // subtree hidden
		// Persisted so it stays collapsed next visit.
		expect(JSON.parse(window.localStorage.getItem("ao.jira.browseCollapsed")!)).toContain("DEMO-1");
	});

	it("renders an Epic as a context-only group header — no status pill / + / send", () => {
		const data: Row[] = [
			{
				key: "DEMO-100",
				type: "Epic",
				title: "Big epic",
				status: "In Progress",
				statusCategory: "indeterminate",
				assignee: "",
			},
			{
				key: "DEMO-1",
				type: "Story",
				title: "A story",
				status: "To Do",
				statusCategory: "new",
				assignee: "Alex",
				parent: { key: "DEMO-100", title: "Big epic" },
			},
		];
		setSearch({ data });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));
		// Flatten grouping so the epic heads the tree.
		fireEvent.click(screen.getByRole("button", { name: "Group by sprint" }));

		expect(screen.getByText("EPIC")).toBeTruthy();
		// The epic has no start actions of its own…
		expect(screen.queryByRole("button", { name: "Create a session for DEMO-100" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Send DEMO-100 to the Orchestrator" })).toBeNull();
		// …but its child story does.
		expect(screen.getByRole("button", { name: "Create a session for DEMO-1" })).toBeTruthy();
	});

	// ── Fix 3: highlight assignee == me ───────────────────────────────────────────

	it("highlights the viewer's own rows with a You chip", () => {
		myselfMock.mockReturnValue({ data: { accountId: "acc-alex" } });
		setSearch({ data: richData });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		// DEMO-1 is Alex's (== me) → You chip; Sam's rows keep the name.
		expect(screen.getByText("You")).toBeTruthy();
		expect(screen.getAllByText("Sam").length).toBeGreaterThan(0);
	});

	// ── Fix 4: Send to Orchestrator + multi-select ────────────────────────────────

	it("labels the per-row Send button with a visible 'Send' next to its icon", () => {
		orchMock.mockReturnValue({ id: "proj-1-orchestrator" });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		const send = screen.getByRole("button", { name: "Send DEMO-101 to the Orchestrator" });
		expect(send.textContent).toContain("Send");
	});

	it("sends a single issue to the project's orchestrator", async () => {
		orchMock.mockReturnValue({ id: "proj-1-orchestrator" });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.click(screen.getByRole("button", { name: "Send DEMO-101 to the Orchestrator" }));
		await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(1));
		const [path, opts] = sendMock.mock.calls[0];
		expect(path).toBe("/api/v1/sessions/{sessionId}/send");
		expect(opts.params.path.sessionId).toBe("proj-1-orchestrator");
		expect(opts.body.message).toContain("DEMO-101");
	});

	it("batch-sends the selected issues in one message", async () => {
		orchMock.mockReturnValue({ id: "proj-1-orchestrator" });
		setSearch({ data: richData });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.click(screen.getByRole("checkbox", { name: /Select DEMO-1 / }));
		fireEvent.click(screen.getByRole("checkbox", { name: /Select DEMO-4 / }));
		expect(screen.getByText("2 selected")).toBeTruthy();
		fireEvent.click(screen.getByRole("button", { name: "Send 2 selected to the Orchestrator" }));

		await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(1));
		const message = sendMock.mock.calls[0][1].body.message as string;
		expect(message).toContain("DEMO-1");
		expect(message).toContain("DEMO-4");
	});

	it("disables Send and warns when the project has no orchestrator", () => {
		orchMock.mockReturnValue(undefined);
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		const send = screen.getByRole("button", { name: "Send DEMO-101 to the Orchestrator" }) as HTMLButtonElement;
		expect(send.disabled).toBe(true);
		// Selecting a row surfaces the warning in the batch bar.
		fireEvent.click(screen.getByRole("checkbox", { name: /Select DEMO-101 / }));
		expect(within(screen.getByRole("group", { name: "Batch actions" })).getByText(/Orchestrator first/i)).toBeTruthy();
		expect(sendMock).not.toHaveBeenCalled();
	});

	it("restores advanced JQL mode + text on return (no project needed)", () => {
		window.localStorage.setItem(
			"ao.jira.browsePrefs",
			JSON.stringify({
				groupBySprint: true,
				assignee: "",
				hideDone: false,
				activeSprintOnly: false,
				advancedMode: true,
				advancedJql: "project = STAR",
			}),
		);
		setSearch({ data: richData });
		renderPage();

		const jql = screen.getByLabelText("Advanced JQL query") as HTMLInputElement;
		expect(jql.value).toBe("project = STAR");
		expect(screen.queryByLabelText("Filter by assignee")).toBeNull();
	});
});
