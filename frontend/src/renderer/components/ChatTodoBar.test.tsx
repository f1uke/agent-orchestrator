import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Todo } from "../lib/chat-inspect";
import { ChatTodoBar } from "./ChatTodoBar";

const todo = (content: string, status: Todo["status"]): Todo => ({ content, activeForm: `${content}…`, status });
const list = [
	todo("Define the contract", "completed"),
	todo("Write index.html", "in_progress"),
	todo("Write styles.css", "in_progress"),
	todo("Check class names", "pending"),
	todo("Summarize", "pending"),
];

describe("ChatTodoBar", () => {
	it("is collapsed by default and counts each status", () => {
		render(<ChatTodoBar todos={list} />);
		const bar = screen.getByRole("button", { name: /To-dos/ });
		expect(bar).toHaveAttribute("aria-expanded", "false");
		expect(bar).toHaveTextContent(/^To-dos1 completed · 2 in progress · 2 pending$/);
		expect(screen.queryByRole("list", { name: "To-dos" })).toBeNull();
	});

	it("opens to one row per todo with its status mark, and closes again", async () => {
		render(<ChatTodoBar todos={list} />);
		const bar = screen.getByRole("button", { name: /To-dos/ });
		await userEvent.click(bar);
		const rows = screen.getAllByRole("listitem");
		expect(rows.map((r) => [r.textContent, r.getAttribute("data-status")])).toEqual([
			["Define the contract", "completed"],
			["Write index.html", "in_progress"],
			["Write styles.css", "in_progress"],
			["Check class names", "pending"],
			["Summarize", "pending"],
		]);
		await userEvent.click(bar);
		expect(screen.queryAllByRole("listitem")).toEqual([]);
	});

	it("leaves a zero count out", () => {
		render(<ChatTodoBar todos={[todo("a", "in_progress"), todo("b", "pending")]} />);
		expect(screen.getByRole("button", { name: /To-dos/ })).toHaveTextContent(/^To-dos1 in progress · 1 pending$/);
	});

	it("renders nothing with no todos", () => {
		const { container } = render(<ChatTodoBar todos={undefined} />);
		expect(container).toBeEmptyDOMElement();
	});
});
