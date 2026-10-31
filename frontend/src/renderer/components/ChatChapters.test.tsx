import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Chapter } from "../lib/chat-chapters";
import { ChatChapters } from "./ChatChapters";

const CHAPTERS: Chapter[] = [
	{ key: "i0", title: "Session start", summary: "" },
	{ key: "i4", title: "Codebase exploration", summary: "Find where calls become rows" },
	{ key: "i9", title: "Widget renderer", summary: "" },
];

const open = async (active = 1, onJump = vi.fn()) => {
	const user = userEvent.setup();
	render(<ChatChapters active={active} chapters={CHAPTERS} onJump={onJump} />);
	await user.click(screen.getByRole("button", { name: /Codebase exploration|Widget renderer|Session start/ }));
	return { user, onJump };
};

describe("ChatChapters", () => {
	it("draws nothing for fewer than two chapters", () => {
		const { container } = render(<ChatChapters active={0} chapters={CHAPTERS.slice(0, 1)} onJump={vi.fn()} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("starts closed with the current chapter on its button", () => {
		render(<ChatChapters active={1} chapters={CHAPTERS} onJump={vi.fn()} />);
		const nav = screen.getByRole("navigation", { name: "Chapters" });
		expect(within(nav).getByRole("button", { expanded: false })).toHaveTextContent("Codebase exploration");
		expect(within(nav).queryByRole("list")).toBeNull();
	});

	it("marks only the current chapter with aria-current", async () => {
		await open(2);
		const items = within(screen.getByRole("list")).getAllByRole("button");
		expect(items.map((b) => b.getAttribute("aria-current"))).toEqual([null, null, "true"]);
	});

	it("jumps to the row of the clicked chapter and closes", async () => {
		const { user, onJump } = await open();
		await user.click(screen.getByRole("button", { name: "Widget renderer" }));
		expect(onJump).toHaveBeenCalledExactlyOnceWith("i9");
		expect(screen.queryByRole("list")).toBeNull();
	});

	it("closes on Escape and gives focus back to the button", async () => {
		const { user } = await open();
		expect(screen.getByRole("list")).toBeInTheDocument();
		await user.keyboard("{Escape}");
		expect(screen.queryByRole("list")).toBeNull();
		expect(screen.getByRole("button", { expanded: false })).toHaveFocus();
	});

	it("shows the summary as the item's title", async () => {
		await open();
		expect(within(screen.getByRole("list")).getByTitle("Find where calls become rows")).toBeInTheDocument();
	});
});
