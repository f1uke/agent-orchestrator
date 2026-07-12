import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { projectsMock } = vi.hoisted(() => ({ projectsMock: vi.fn() }));
vi.mock("../hooks/useSessionJiraContext", () => ({ useJiraProjects: projectsMock }));

import { JiraProjectPicker } from "./JiraProjectPicker";
import { readStarredProjects } from "../lib/jira-starred-projects";

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
		localStorage.clear();
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
		expect(screen.getByText(/2 projects · ★ star to pin/i)).toBeTruthy();

		fireEvent.click(screen.getByText("Acme Platform"));
		expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ key: "ACME" }));
	});

	it("stars a project, pins it to the top group, and persists it", () => {
		render(<JiraProjectPicker value={null} onSelect={vi.fn()} />);
		fireEvent.click(screen.getByRole("button", { name: /select a project/i }));

		// No favorites yet — no Starred group.
		expect(screen.queryByText("Starred")).toBeNull();

		fireEvent.click(screen.getByRole("button", { name: "Star ACME" }));

		// The favorite pins to a "Starred" group above "All projects"…
		expect(screen.getByText("Starred")).toBeTruthy();
		expect(screen.getByText("All projects")).toBeTruthy();
		const acme = screen.getByText("Acme Platform");
		const demo = screen.getByText("Demo Project");
		expect(acme.compareDocumentPosition(demo) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

		// …and persists to localStorage.
		expect([...readStarredProjects()]).toContain("ACME");
		// The row's star now reads as pressed (its label flips to Unstar).
		expect(screen.getByRole("button", { name: "Unstar ACME" })).toBeTruthy();
	});

	it("restores starred projects from localStorage and unstars on toggle", () => {
		localStorage.setItem("ao.jira.starredProjects", JSON.stringify(["ACME"]));
		render(<JiraProjectPicker value={null} onSelect={vi.fn()} />);
		fireEvent.click(screen.getByRole("button", { name: /select a project/i }));

		// The remembered favorite shows in the Starred group on open.
		expect(screen.getByText("Starred")).toBeTruthy();
		expect(screen.getByRole("button", { name: "Unstar ACME" })).toBeTruthy();

		fireEvent.click(screen.getByRole("button", { name: "Unstar ACME" }));
		expect(screen.queryByText("Starred")).toBeNull();
		expect([...readStarredProjects()]).not.toContain("ACME");
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
