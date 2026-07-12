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

function setSearch(over: Record<string, unknown> = {}) {
	searchMock.mockReturnValue({
		data: [
			{
				key: "DEMO-101",
				type: "Story",
				title: "Story one",
				status: "Ready for QA",
				statusCategory: "new",
				assignee: "Alex",
			},
			{ key: "DEMO-88", type: "Bug", title: "Bug two", status: "To Do", statusCategory: "new", assignee: "" },
		],
		isFetching: false,
		isError: false,
		error: null,
		...over,
	});
}

// A richer set spanning two sprints + a no-sprint issue, for the grouping /
// assignee tests.
const richData = [
	{
		key: "DEMO-1",
		type: "Story",
		title: "S one",
		status: "To Do",
		statusCategory: "new",
		assignee: "Alex",
		sprint: { name: "Sprint 2026-14", state: "active" },
	},
	{
		key: "DEMO-4",
		type: "Story",
		title: "S four",
		status: "To Do",
		statusCategory: "new",
		assignee: "Sam",
		sprint: { name: "Sprint 2026-14", state: "active" },
	},
	{
		key: "DEMO-2",
		type: "Story",
		title: "S two",
		status: "To Do",
		statusCategory: "new",
		assignee: "Sam",
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
		expect(JSON.parse(window.localStorage.getItem("ao.jira.browsePrefs")!).assignee).toBe("Sam");
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
});
