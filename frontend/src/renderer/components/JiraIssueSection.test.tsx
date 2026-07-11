import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { useJiraMock } = vi.hoisted(() => ({ useJiraMock: vi.fn() }));
vi.mock("../hooks/useSessionJiraContext", () => ({
	useSessionJiraContext: useJiraMock,
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
		title: "Order Eligible UI",
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
	beforeEach(() => useJiraMock.mockReset());

	it("renders nothing when the session is not Jira-linked (and never queries)", () => {
		mockQuery(undefined);
		const { container } = render(<JiraIssueSection sessionId="s1" linked={false} />);
		expect(container.firstChild).toBeNull();
		// enabled=false is passed through so the hook does not fetch.
		expect(useJiraMock).toHaveBeenCalledWith("s1", false);
	});

	it("renders the issue lead, description, and subtasks when linked", () => {
		mockQuery(fullIssue);
		render(<JiraIssueSection sessionId="s1" linked={true} />);
		expect(screen.getByText("DEMO-101")).toBeTruthy();
		expect(screen.getByText("Order Eligible UI")).toBeTruthy();
		expect(screen.getByText("Ready for QA")).toBeTruthy();
		expect(screen.getByText("Alex")).toBeTruthy();
		expect(screen.getByText("Some requirement text.")).toBeTruthy();
		expect(screen.getByText("DEMO-102")).toBeTruthy();
		expect(screen.getByText("Subtasks · 1")).toBeTruthy();
		const open = screen.getByRole("link", { name: /open in jira/i });
		expect(open.getAttribute("href")).toBe("https://example.atlassian.net/browse/DEMO-101");
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
