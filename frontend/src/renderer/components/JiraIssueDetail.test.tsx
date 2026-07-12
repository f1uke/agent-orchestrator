import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { issueMock } = vi.hoisted(() => ({ issueMock: vi.fn() }));

// Only useJiraIssue is exercised here; the drawer's Move-status uses the by-key
// dialog, which we stub below.
vi.mock("../hooks/useSessionJiraContext", () => ({ useJiraIssue: issueMock }));

// Stub the ADF renderer (its own tests cover it) so the description just marks itself.
vi.mock("./JiraAdf", () => ({
	JiraAdf: ({ nodes }: { nodes: unknown[] }) => <div data-testid="adf">adf:{nodes.length}</div>,
}));

// Stub the by-key Move-status dialog so we can assert it opens for the right key
// without pulling in the transitions/move hooks.
vi.mock("./JiraMoveStatusDialog", () => ({
	JiraIssueMoveDialog: ({ target, open }: { target: { key: string }; open: boolean }) =>
		open ? <div data-testid="move">move:{target.key}</div> : null,
}));

import { JiraIssueDetail } from "./JiraIssueDetail";

const PARENT = {
	key: "DEMO-101",
	type: "Story",
	title: "Parent story",
	status: "To Do",
	statusCategory: "new",
	assignee: "Alex Rivera",
	url: "https://example.atlassian.net/browse/DEMO-101",
	description: [{ type: "paragraph" }],
	subtasks: [
		{
			key: "DEMO-102",
			type: "Sub-task",
			title: "Child subtask",
			status: "In Progress",
			statusCategory: "indeterminate",
		},
	],
};

const CHILD = {
	key: "DEMO-102",
	type: "Sub-task",
	title: "Child subtask",
	status: "In Progress",
	statusCategory: "indeterminate",
	assignee: "Sam Chen",
	reporter: "Alex Rivera",
	priority: "High",
	url: "https://example.atlassian.net/browse/DEMO-102",
	parent: { key: "DEMO-101", title: "Parent story" },
	description: [],
	subtasks: [],
};

// Return the issue matching the requested key (the arg the component passes as it
// navigates via the breadcrumb / subtask rows), so navigation re-fetches correctly.
type Issue = typeof PARENT | typeof CHILD;
function setIssues(pool: Issue[], over: Record<string, unknown> = {}) {
	issueMock.mockImplementation((key?: string) => ({
		data: key ? (pool.find((i) => i.key === key) ?? null) : null,
		isLoading: false,
		isError: false,
		error: null,
		...over,
	}));
}

function renderDetail(issueKey: string | null, onCreateSession = vi.fn()) {
	render(
		<JiraIssueDetail
			issueKey={issueKey}
			open={Boolean(issueKey)}
			onOpenChange={vi.fn()}
			onCreateSession={onCreateSession}
		/>,
	);
	return { onCreateSession };
}

describe("JiraIssueDetail", () => {
	beforeEach(() => {
		issueMock.mockReset();
		setIssues([PARENT, CHILD]);
	});

	it("renders the issue lead read-only, with the description and subtasks", () => {
		renderDetail("DEMO-101");
		expect(screen.getByText("DEMO-101")).toBeTruthy();
		expect(screen.getByText("Parent story")).toBeTruthy();
		expect(screen.getByText("Alex Rivera")).toBeTruthy();
		expect(screen.getByRole("link", { name: /Open in Jira/i })).toBeTruthy();
		expect(screen.getByTestId("adf")).toHaveTextContent("adf:1");
		// The subtask shows in the Subtasks card.
		expect(screen.getByRole("button", { name: "DEMO-102" })).toBeTruthy();
	});

	it("shows a parent breadcrumb for a subtask and navigates into the parent on click", () => {
		renderDetail("DEMO-102");
		// The subtask opens with a clickable parent breadcrumb (mockup #36).
		const crumb = screen.getByRole("button", { name: "Open parent DEMO-101" });
		expect(crumb).toBeTruthy();
		fireEvent.click(crumb);
		// Now the drawer shows the parent (its subtask list reappears).
		expect(screen.getByText("Parent story")).toBeTruthy();
		expect(screen.getByRole("button", { name: "DEMO-102" })).toBeTruthy();
		// The parent has no parent, so the breadcrumb is gone.
		expect(screen.queryByRole("button", { name: /Open parent/i })).toBeNull();
	});

	it("navigates into a subtask when its key is clicked", () => {
		renderDetail("DEMO-101");
		fireEvent.click(screen.getByRole("button", { name: "DEMO-102" }));
		// The child's parent breadcrumb confirms we drilled in.
		expect(screen.getByRole("button", { name: "Open parent DEMO-101" })).toBeTruthy();
	});

	it("opens the by-key Move-status dialog from the status pill", () => {
		renderDetail("DEMO-102");
		expect(screen.queryByTestId("move")).toBeNull();
		fireEvent.click(screen.getByRole("button", { name: /In Progress/ }));
		expect(screen.getByTestId("move")).toHaveTextContent("move:DEMO-102");
	});

	it("hands off to Create session with the issue summary", () => {
		const { onCreateSession } = renderDetail("DEMO-102");
		fireEvent.click(screen.getByRole("button", { name: /Create session/i }));
		expect(onCreateSession).toHaveBeenCalledWith(expect.objectContaining({ key: "DEMO-102" }));
	});

	it("surfaces a load failure inline", () => {
		setIssues([], { isError: true, error: new Error("set JIRA_API_TOKEN") });
		renderDetail("DEMO-999");
		expect(screen.getByText(/set JIRA_API_TOKEN/i)).toBeTruthy();
	});

	it("shows a not-found message when the issue is missing", () => {
		setIssues([]);
		renderDetail("DEMO-404");
		expect(screen.getByText(/Jira issue not found/i)).toBeTruthy();
	});
});
