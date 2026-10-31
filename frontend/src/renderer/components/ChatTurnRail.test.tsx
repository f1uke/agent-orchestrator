import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { RailTurn } from "../lib/chat-rail";
import { ChatTurnRail } from "./ChatTurnRail";

const turns: RailTurn[] = [
	{ turn: 1, key: "i0", prompt: "Fix the ledger tests", reply: "Fixed both bugs." },
	{ turn: 2, key: "i9", prompt: "Add a test", reply: "Added TestTransferAtomic." },
	{ turn: 3, key: "i20", prompt: "Ship it", reply: "" },
];

const ticks = () => screen.getAllByRole("button");

describe("ChatTurnRail", () => {
	it("is absent with fewer than two turns", () => {
		const { container } = render(<ChatTurnRail active={0} onJump={() => undefined} turns={turns.slice(0, 1)} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("names every tick by its turn and prompt, and marks the current turn", () => {
		render(<ChatTurnRail active={1} onJump={() => undefined} turns={turns} />);
		expect(screen.getByRole("navigation", { name: "Turns" })).toBeInTheDocument();
		expect(ticks().map((t) => t.getAttribute("aria-label"))).toEqual([
			"Turn 1: Fix the ledger tests",
			"Turn 2: Add a test",
			"Turn 3: Ship it",
		]);
		expect(ticks().map((t) => t.getAttribute("aria-current"))).toEqual([null, "true", null]);
	});

	it("is one Tab stop, on the current turn, and moves with the arrows, Home and End", async () => {
		const user = userEvent.setup();
		render(<ChatTurnRail active={1} onJump={() => undefined} turns={turns} />);
		expect(ticks().map((t) => t.tabIndex)).toEqual([-1, 0, -1]);
		await user.tab();
		expect(ticks()[1]).toHaveFocus();
		await user.keyboard("{ArrowDown}");
		expect(ticks()[2]).toHaveFocus();
		expect(ticks().map((t) => t.tabIndex)).toEqual([-1, -1, 0]);
		await user.keyboard("{ArrowDown}");
		expect(ticks()[2]).toHaveFocus();
		await user.keyboard("{Home}");
		expect(ticks()[0]).toHaveFocus();
		await user.keyboard("{ArrowUp}");
		expect(ticks()[0]).toHaveFocus();
		await user.keyboard("{End}");
		expect(ticks()[2]).toHaveFocus();
	});

	it("jumps to a turn on a click, Enter or Space", async () => {
		const user = userEvent.setup();
		const onJump = vi.fn();
		render(<ChatTurnRail active={2} onJump={onJump} turns={turns} />);
		await user.click(ticks()[0]);
		expect(onJump).toHaveBeenLastCalledWith("i0");
		ticks()[1].focus();
		await user.keyboard("{Enter}");
		expect(onJump).toHaveBeenLastCalledWith("i9");
		await user.keyboard("{ArrowDown}{ }");
		expect(onJump).toHaveBeenLastCalledWith("i20");
	});

	it("previews a hovered or focused turn's prompt and last reply in a card the tick names", async () => {
		const user = userEvent.setup();
		render(<ChatTurnRail active={0} onJump={() => undefined} turns={turns} />);
		expect(screen.queryByRole("tooltip")).toBeNull();
		await user.hover(ticks()[1]);
		const card = screen.getByRole("tooltip");
		expect(card).toHaveTextContent(/^Turn 2\s*Add a test\s*Added TestTransferAtomic\.$/);
		expect(ticks()[1]).toHaveAttribute("aria-describedby", card.id);
		act(() => ticks()[2].focus());
		expect(screen.getByRole("tooltip")).toHaveTextContent(/^Turn 3\s*Ship it$/);
	});
});
