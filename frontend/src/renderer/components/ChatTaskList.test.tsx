import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Todo } from "../lib/chat-inspect";
import { ChatTaskList } from "./ChatTaskList";

const todos: Todo[] = [
	{ content: "Read the fold", activeForm: "Reading the fold", status: "completed" },
	{ content: "Add the frame", activeForm: "Adding the frame", status: "completed" },
	{ content: "Wire the composer", activeForm: "Wiring the composer", status: "in_progress" },
	{ content: "Take screenshots", activeForm: "Taking screenshots", status: "pending" },
];

describe("ChatTaskList", () => {
	it("counts the completed tasks against the total", () => {
		render(<ChatTaskList todos={todos} />);
		expect(screen.getByText("Tasks · 2 of 4")).toBeInTheDocument();
	});

	it("lists every task, naming the running one by its active form", () => {
		render(<ChatTaskList todos={todos} />);
		const items = within(screen.getByRole("list", { name: "Tasks" })).getAllByRole("listitem");
		expect(items.map((i) => i.textContent)).toEqual([
			"Read the fold",
			"Add the frame",
			"Wiring the composer",
			"Take screenshots",
		]);
		expect(items.map((i) => i.getAttribute("data-status"))).toEqual([
			"completed",
			"completed",
			"in_progress",
			"pending",
		]);
	});

	it("falls back to the content when the running task has no active form", () => {
		render(<ChatTaskList todos={[{ content: "Wire it", activeForm: "", status: "in_progress" }]} />);
		expect(screen.getByRole("listitem")).toHaveTextContent("Wire it");
	});

	it("has nothing to click", () => {
		render(<ChatTaskList todos={todos} />);
		expect(screen.queryByRole("button")).toBeNull();
	});
});
