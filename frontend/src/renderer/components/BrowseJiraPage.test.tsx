import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { searchMock, navigateMock } = vi.hoisted(() => ({ searchMock: vi.fn(), navigateMock: vi.fn() }));

vi.mock("../hooks/useSessionJiraContext", () => ({ useJiraSearch: searchMock }));
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

// The real search pushes assignee (accountId / "unassigned") + type into the JQL;
// the component now calls useJiraSearch twice — an UNFILTERED base fetch (opts
// undefined, feeds the assignee dropdown) and a FILTERED results fetch (opts set).
// This mirrors that server-side filtering so the mock returns what Jira would.
function applyServerFilters(data: Row[], opts?: { assignee?: string; types?: string[]; jql?: string }): Row[] {
	if (!opts) return data; // base fetch — unfiltered dropdown source
	// Advanced / parent-inclusion path: only `key in (...)` is interpreted here.
	if (opts.jql) {
		const m = opts.jql.match(/key in \(([^)]*)\)/i);
		if (m) {
			const keys = new Set(m[1].split(",").map((k) => k.trim()));
			return data.filter((it) => keys.has(it.key));
		}
		return data;
	}
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
}

// A richer set spanning two sprints + a no-sprint issue, for the grouping /
// assignee tests. Assignees carry their accountId (what the server filter keys on).
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
		// The pick is persisted for the next visit.
		expect(window.localStorage.getItem("ao.jira.lastProject")).toContain("DEMO");
	});

	it("filters the list by issue type via the chips", () => {
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.click(screen.getByRole("button", { name: "Bug" }));
		expect(screen.queryByText("DEMO-101")).toBeNull();
		expect(screen.getByText("DEMO-88")).toBeTruthy();
		// The type is pushed into the server-side query (its JQL name), not filtered
		// client-side over a capped page.
		const sawBugType = searchMock.mock.calls.some((call: unknown[]) =>
			(call[3] as { types?: string[] } | undefined)?.types?.includes("Bug"),
		);
		expect(sawBugType).toBe(true);
	});

	it("opens the New-task modal pre-filled when Create session is clicked", () => {
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.click(screen.getAllByRole("button", { name: /Create session/i })[0]);
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
		expect(screen.getByText(/2 work items/)).toBeTruthy(); // Sprint 2026-14 has two
		expect(screen.getByText("active")).toBeTruthy(); // active-sprint badge

		// Collapsing a sprint hides its rows but keeps the header.
		fireEvent.click(screen.getByRole("button", { name: /Sprint 2026-14/ }));
		expect(screen.queryByText("DEMO-1")).toBeNull();
		expect(screen.getByText("Sprint 2026-14")).toBeTruthy();
	});

	it("toggles grouping off to a flat list and remembers it", () => {
		setSearch({ data: richData });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.click(screen.getByRole("button", { name: "Group by sprint" }));
		expect(screen.queryByText("Sprint 2026-14")).toBeNull(); // no section headers
		expect(screen.getByText("DEMO-1")).toBeTruthy(); // rows still render flat
		expect(JSON.parse(window.localStorage.getItem("ao.jira.browsePrefs")!).groupBySprint).toBe(false);
	});

	it("filters the list by assignee", () => {
		setSearch({ data: richData });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.change(screen.getByLabelText("Filter by assignee"), { target: { value: "Sam" } });
		expect(screen.getByText("DEMO-4")).toBeTruthy();
		expect(screen.getByText("DEMO-2")).toBeTruthy();
		expect(screen.queryByText("DEMO-1")).toBeNull(); // Alex's
		expect(screen.queryByText("DEMO-3")).toBeNull(); // unassigned
		// The display name is persisted (human-readable, back-compat)…
		expect(JSON.parse(window.localStorage.getItem("ao.jira.browsePrefs")!).assignee).toBe("Sam");
		// …but the query carries Sam's opaque accountId, so the filter runs in the
		// JQL and returns all of Sam's issues rather than a client-pared page (the
		// under-fetch this fix addresses).
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

		// Grouping off (no headers) and Alex's filter applied.
		expect(screen.queryByText("Sprint 2026-14")).toBeNull();
		expect(screen.getByText("DEMO-1")).toBeTruthy();
		expect(screen.queryByText("DEMO-4")).toBeNull(); // Sam's, filtered out
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
		// Structured filters gone; the JQL box appears.
		expect(screen.queryByRole("button", { name: "Bug" })).toBeNull();
		expect(screen.queryByLabelText("Filter by assignee")).toBeNull();
		fireEvent.change(screen.getByLabelText("Advanced JQL query"), {
			target: { value: "project = STAR AND labels = urgent" },
		});

		const sawJql = searchMock.mock.calls.some(
			(c: unknown[]) => (c[3] as { jql?: string } | undefined)?.jql === "project = STAR AND labels = urgent",
		);
		expect(sawJql).toBe(true);

		// A clear way back to structured mode.
		fireEvent.click(screen.getByRole("button", { name: /Back to filters/ }));
		expect(screen.getByLabelText("Filter by assignee")).toBeTruthy();
	});

	it("under an assignee filter, pulls in the parent of a matched subtask as dimmed context", () => {
		// Sam owns a subtask whose parent story is Alex's. Filtering to Sam must still
		// show the parent (for hierarchy), fetched via the key-in context query.
		const hierData: Row[] = [
			{
				key: "DEMO-101",
				type: "Story",
				title: "Parent story",
				status: "To Do",
				statusCategory: "new",
				assignee: "Alex",
				assigneeAccountId: "acc-alex",
			},
			{
				key: "DEMO-102",
				type: "Sub-task",
				title: "Sam's subtask",
				status: "To Do",
				statusCategory: "new",
				assignee: "Sam",
				assigneeAccountId: "acc-sam",
				parent: { key: "DEMO-101", title: "Parent story" },
			},
		];
		setSearch({ data: hierData });
		renderPage();
		fireEvent.click(screen.getByText("picker:none"));

		fireEvent.change(screen.getByLabelText("Filter by assignee"), { target: { value: "Sam" } });

		// The matched subtask AND its (context) parent both render…
		expect(screen.getByText("DEMO-102")).toBeTruthy();
		expect(screen.getByText("DEMO-101")).toBeTruthy();
		// …with the parent marked as context, not a direct match.
		expect(screen.getByText(/parent · context/i)).toBeTruthy();
		// The context parent was fetched via a `key in (...)` query.
		const sawKeyIn = searchMock.mock.calls.some((c: unknown[]) =>
			(c[3] as { jql?: string } | undefined)?.jql?.includes("key in (DEMO-101)"),
		);
		expect(sawKeyIn).toBe(true);
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
