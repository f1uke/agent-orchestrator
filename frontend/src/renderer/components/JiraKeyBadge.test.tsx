import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { useJiraMock } = vi.hoisted(() => ({ useJiraMock: vi.fn() }));
vi.mock("../hooks/useSessionJiraContext", () => ({
	useSessionJiraContext: useJiraMock,
}));

import { JiraKeyBadge } from "./JiraKeyBadge";
import type { JiraContext } from "../hooks/useSessionJiraContext";

function mockQuery(data: JiraContext | undefined) {
	useJiraMock.mockReturnValue({ data });
}

const linked: JiraContext = {
	sessionId: "s1",
	linked: true,
	issue: {
		key: "DEMO-101",
		url: "https://example.atlassian.net/browse/DEMO-101",
		type: "Story",
		title: "Order Eligible UI",
		status: "Ready for QA",
		statusCategory: "new",
		description: [],
		subtasks: [],
	},
};

describe("JiraKeyBadge", () => {
	beforeEach(() => useJiraMock.mockReset());

	it("always enables the query (KEY is always Jira-bound here)", () => {
		mockQuery(undefined);
		render(<JiraKeyBadge sessionId="s1" issueKey="DEMO-101" />);
		expect(useJiraMock).toHaveBeenCalledWith("s1", true);
	});

	it("shows the KEY immediately even before the context loads (no status yet)", () => {
		mockQuery(undefined);
		render(<JiraKeyBadge sessionId="s1" issueKey="DEMO-101" />);
		expect(screen.getByText("DEMO-101")).toBeTruthy();
		expect(screen.queryByText("Ready for QA")).toBeNull();
	});

	it("shows KEY + status once the issue loads, with a type square (card variant)", () => {
		mockQuery(linked);
		const { container } = render(<JiraKeyBadge sessionId="s1" issueKey="DEMO-101" variant="card" />);
		expect(screen.getByText("DEMO-101")).toBeTruthy();
		expect(screen.getByText("Ready for QA")).toBeTruthy();
		expect(container.querySelector(".jira-badge__sq")).not.toBeNull();
		expect(container.querySelector(".jira-badge__diamond")).toBeNull();
	});

	it("uses the compact diamond (no type square) in the row variant", () => {
		mockQuery(linked);
		const { container } = render(<JiraKeyBadge sessionId="s1" issueKey="DEMO-101" variant="row" />);
		expect(container.querySelector(".jira-badge--row")).not.toBeNull();
		expect(container.querySelector(".jira-badge__diamond")).not.toBeNull();
		expect(container.querySelector(".jira-badge__sq")).toBeNull();
	});

	it("shows the KEY alone (no status) when linked but the Jira fetch failed", () => {
		mockQuery({ sessionId: "s1", linked: true, fetchError: "Couldn't reach Jira." });
		render(<JiraKeyBadge sessionId="s1" issueKey="DEMO-101" />);
		expect(screen.getByText("DEMO-101")).toBeTruthy();
		expect(screen.queryByText("Ready for QA")).toBeNull();
	});
});
