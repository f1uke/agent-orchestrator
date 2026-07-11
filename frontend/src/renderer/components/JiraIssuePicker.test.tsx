import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { searchMock } = vi.hoisted(() => ({ searchMock: vi.fn() }));
vi.mock("../hooks/useSessionJiraContext", () => ({ useJiraSearch: searchMock }));

import { JiraIssuePicker } from "./JiraIssuePicker";

function setSearch(over: Record<string, unknown> = {}) {
	searchMock.mockReturnValue({
		data: [
			{
				key: "DEMO-2272",
				type: "Story",
				title: "Example issue summary",
				status: "Ready for QA",
				statusCategory: "new",
			},
			{ key: "DEMO-88", type: "Bug", title: "Example bug summary", status: "To Do", statusCategory: "new" },
		],
		isFetching: false,
		isError: false,
		error: null,
		...over,
	});
}

describe("JiraIssuePicker", () => {
	beforeEach(() => {
		searchMock.mockReset();
		setSearch();
	});

	it("hides the results dropdown until the query is long enough", () => {
		render(<JiraIssuePicker query="s" onQueryChange={vi.fn()} onPick={vi.fn()} />);
		expect(screen.queryByRole("listbox")).toBeNull();
	});

	it("lists live results and calls onPick with the chosen issue", () => {
		const onPick = vi.fn();
		render(<JiraIssuePicker query="demo" onQueryChange={vi.fn()} onPick={onPick} />);
		expect(screen.getByText("DEMO-2272")).toBeTruthy();
		expect(screen.getByText("Example bug summary")).toBeTruthy();
		fireEvent.click(screen.getByText("Example issue summary"));
		expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ key: "DEMO-2272" }));
	});

	it("surfaces a search failure (e.g. a missing token)", () => {
		setSearch({ data: undefined, isError: true, error: new Error("set JIRA_API_TOKEN") });
		render(<JiraIssuePicker query="demo" onQueryChange={vi.fn()} onPick={vi.fn()} />);
		expect(screen.getByText(/set JIRA_API_TOKEN/i)).toBeTruthy();
	});

	it("shows an empty note when nothing matches", () => {
		setSearch({ data: [] });
		render(<JiraIssuePicker query="zzz" onQueryChange={vi.fn()} onPick={vi.fn()} />);
		expect(screen.getByText(/No matching issues/i)).toBeTruthy();
	});
});
