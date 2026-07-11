import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { searchMock, bindMock, mutateMock } = vi.hoisted(() => ({
	searchMock: vi.fn(),
	bindMock: vi.fn(),
	mutateMock: vi.fn(),
}));
vi.mock("../hooks/useSessionJiraContext", () => ({
	useJiraSearch: searchMock,
	useSetJiraBinding: bindMock,
}));

import { JiraLinkDialog } from "./JiraLinkDialog";

function setSearch() {
	searchMock.mockReturnValue({
		data: [
			{
				key: "DEMO-2272",
				type: "Story",
				title: "Example issue summary",
				status: "Ready for QA",
				statusCategory: "new",
			},
		],
		isFetching: false,
		isError: false,
		error: null,
	});
}

function setBind(over: Record<string, unknown> = {}) {
	bindMock.mockReturnValue({
		mutate: mutateMock,
		reset: vi.fn(),
		isPending: false,
		isError: false,
		error: null,
		...over,
	});
}

describe("JiraLinkDialog", () => {
	beforeEach(() => {
		searchMock.mockReset();
		bindMock.mockReset();
		mutateMock.mockReset();
		setSearch();
		setBind();
	});

	it("binds the picked issue on confirm", () => {
		render(<JiraLinkDialog sessionId="s1" open={true} onOpenChange={vi.fn()} />);
		// Confirm is disabled until an issue is picked.
		const link = screen.getByRole("button", { name: /link issue/i }) as HTMLButtonElement;
		expect(link.disabled).toBe(true);
		// Type to reveal results, then pick the issue.
		fireEvent.change(screen.getByRole("textbox"), { target: { value: "demo" } });
		fireEvent.click(screen.getByText("Example issue summary"));
		expect(link.disabled).toBe(false);
		fireEvent.click(link);
		expect(mutateMock).toHaveBeenCalledWith("DEMO-2272", expect.anything());
	});

	it("surfaces a link failure", () => {
		setBind({ isError: true, error: new Error("Jira issue not found or not visible") });
		render(<JiraLinkDialog sessionId="s1" open={true} onOpenChange={vi.fn()} />);
		expect(screen.getByText(/not found or not visible/i)).toBeTruthy();
	});
});
