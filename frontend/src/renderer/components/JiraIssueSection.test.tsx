import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { useJiraMock, unlinkMutate } = vi.hoisted(() => ({ useJiraMock: vi.fn(), unlinkMutate: vi.fn() }));
vi.mock("../hooks/useSessionJiraContext", () => ({
	useSessionJiraContext: useJiraMock,
	// The lead now mounts the (closed) Move-status dialog + link/unlink affordances,
	// whose hooks run on render; stub them so the section tests stay focused on the
	// display.
	useJiraTransitions: () => ({ data: [], isLoading: false, isError: false, error: null }),
	useMoveJiraStatus: () => ({ mutate: vi.fn(), reset: vi.fn(), isPending: false, isError: false, error: null }),
	useUnlinkJira: () => ({ mutate: unlinkMutate, isPending: false, isError: false, error: null }),
	useSetJiraBinding: () => ({ mutate: vi.fn(), reset: vi.fn(), isPending: false, isError: false, error: null }),
	useJiraSearch: () => ({ data: [], isFetching: false, isError: false, error: null }),
}));

import { JiraIssueSection } from "./JiraIssueSection";
import type { JiraContext } from "../hooks/useSessionJiraContext";

function mockQuery(data: JiraContext | undefined, isLoading = false) {
	useJiraMock.mockReturnValue({ data, isLoading });
}

const fullIssue: JiraContext = {
	sessionId: "s1",
	linked: true,
	issue: {
		key: "DEMO-101",
		url: "https://example.atlassian.net/browse/DEMO-101",
		type: "Story",
		title: "Example issue summary",
		status: "Ready for QA",
		statusCategory: "new",
		priority: "Medium",
		assignee: "Alex",
		reporter: "Sam",
		sprint: {
			name: "Sprint 2026-14",
			state: "active",
			startDate: "2026-06-29T00:00:00Z",
			endDate: "2026-07-10T00:00:00Z",
		},
		description: [{ type: "paragraph", content: [{ type: "text", text: "Some requirement text." }] }],
		subtasks: [
			{ key: "DEMO-102", type: "Sub-task", title: "iOS", status: "Pull Request", statusCategory: "indeterminate" },
		],
	},
};

describe("JiraIssueSection", () => {
	beforeEach(() => {
		useJiraMock.mockReset();
		unlinkMutate.mockReset();
	});

	it("offers a link prompt when the session is not Jira-linked (and never queries)", () => {
		mockQuery(undefined);
		render(<JiraIssueSection sessionId="s1" linked={false} />);
		// An unlinked session gets an after-the-fact link entry point, not nothing.
		expect(screen.getByText(/No Jira issue linked/i)).toBeTruthy();
		expect(screen.getByRole("button", { name: /Link a Jira issue/i })).toBeTruthy();
		// enabled=false is passed through so the display hook does not fetch.
		expect(useJiraMock).toHaveBeenCalledWith("s1", false);
	});

	it("unlinks a linked session from the issue section", () => {
		mockQuery(fullIssue);
		render(<JiraIssueSection sessionId="s1" linked={true} />);
		fireEvent.click(screen.getByRole("button", { name: /^Unlink/i }));
		expect(unlinkMutate).toHaveBeenCalled();
	});

	it("renders the issue lead, description, and subtasks when linked", () => {
		mockQuery(fullIssue);
		render(<JiraIssueSection sessionId="s1" linked={true} />);
		expect(screen.getByText("DEMO-101")).toBeTruthy();
		expect(screen.getByText("Example issue summary")).toBeTruthy();
		expect(screen.getByText("Ready for QA")).toBeTruthy();
		expect(screen.getByText("Alex")).toBeTruthy();
		expect(screen.getByText("Some requirement text.")).toBeTruthy();
		expect(screen.getByText("DEMO-102")).toBeTruthy();
		expect(screen.getByText("Subtasks · 1")).toBeTruthy();
		const open = screen.getByRole("link", { name: /open in jira/i });
		expect(open.getAttribute("href")).toBe("https://example.atlassian.net/browse/DEMO-101");
	});

	it("opens the Move-status dialog from the issue's status pill", () => {
		mockQuery(fullIssue);
		render(<JiraIssueSection sessionId="s1" linked={true} />);
		// The issue's pill is a Move-status entry point (its own status text).
		fireEvent.click(screen.getByRole("button", { name: /Ready for QA/i }));
		expect(screen.getByText(/move jira status/i)).toBeTruthy();
	});

	it("opens the Move-status dialog from a subtask's status pill (movable subtasks)", () => {
		mockQuery(fullIssue);
		render(<JiraIssueSection sessionId="s1" linked={true} />);
		// The subtask's status pill is its own Move-status entry point.
		fireEvent.click(screen.getByRole("button", { name: /Pull Request/i }));
		// The dialog opens targeting the subtask key (now shown in both the row and
		// the dialog).
		expect(screen.getByText(/move jira status/i)).toBeTruthy();
		expect(screen.getAllByText("DEMO-102").length).toBeGreaterThanOrEqual(2);
	});

	it("shows a graceful note when linked but the Jira fetch failed", () => {
		mockQuery({ sessionId: "s1", linked: true, fetchError: "Couldn't reach Jira (jira-cli unavailable)." });
		render(<JiraIssueSection sessionId="s1" linked={true} />);
		expect(screen.getByText(/couldn't reach jira/i)).toBeTruthy();
	});

	it("renders a loading placeholder before the first result", () => {
		mockQuery(undefined, true);
		render(<JiraIssueSection sessionId="s1" linked={true} />);
		expect(screen.getByText(/loading jira issue/i)).toBeTruthy();
	});
});
