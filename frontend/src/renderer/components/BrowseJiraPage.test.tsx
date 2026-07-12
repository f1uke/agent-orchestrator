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
});
