import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { projectsMock } = vi.hoisted(() => ({ projectsMock: vi.fn() }));
vi.mock("../hooks/useSessionJiraContext", () => ({ useJiraProjects: projectsMock }));

import { JiraProjectPicker } from "./JiraProjectPicker";

function setProjects(over: Record<string, unknown> = {}) {
	projectsMock.mockReturnValue({
		data: [
			{ key: "DEMO", name: "Demo Project" },
			{ key: "ACME", name: "Acme Platform" },
		],
		isFetching: false,
		isError: false,
		error: null,
		...over,
	});
}

describe("JiraProjectPicker", () => {
	beforeEach(() => {
		projectsMock.mockReset();
		setProjects();
	});

	it("shows a placeholder until a project is chosen", () => {
		render(<JiraProjectPicker value={null} onSelect={vi.fn()} />);
		expect(screen.getByText("Select a project")).toBeTruthy();
		// Closed by default — no options listed.
		expect(screen.queryByRole("listbox")).toBeNull();
	});

	it("reflects the selected project on the trigger", () => {
		render(<JiraProjectPicker value={{ key: "DEMO", name: "Demo Project" }} onSelect={vi.fn()} />);
		expect(screen.getByText("DEMO")).toBeTruthy();
		expect(screen.getByText("· Demo Project")).toBeTruthy();
	});

	it("opens the dropdown, marks the last-used project, and picks one", () => {
		const onSelect = vi.fn();
		render(<JiraProjectPicker value={null} onSelect={onSelect} lastUsedKey="DEMO" />);

		fireEvent.click(screen.getByRole("button", { name: /select a project/i }));
		expect(screen.getByRole("listbox")).toBeTruthy();
		expect(screen.getByText("Last used")).toBeTruthy();
		expect(screen.getByText(/2 projects · from jira project list/i)).toBeTruthy();

		fireEvent.click(screen.getByText("Acme Platform"));
		expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ key: "ACME" }));
	});

	it("surfaces a load failure (e.g. a missing token)", () => {
		setProjects({ data: undefined, isError: true, error: new Error("set JIRA_API_TOKEN") });
		render(<JiraProjectPicker value={null} onSelect={vi.fn()} />);
		fireEvent.click(screen.getByRole("button", { name: /select a project/i }));
		expect(screen.getByText(/set JIRA_API_TOKEN/i)).toBeTruthy();
	});

	it("shows an empty note when nothing matches", () => {
		setProjects({ data: [] });
		render(<JiraProjectPicker value={null} onSelect={vi.fn()} />);
		fireEvent.click(screen.getByRole("button", { name: /select a project/i }));
		expect(screen.getByText(/No matching projects/i)).toBeTruthy();
	});
});
