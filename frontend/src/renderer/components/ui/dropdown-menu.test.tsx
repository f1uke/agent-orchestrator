import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "./dropdown-menu";

// Stand-in for the xterm surface: focusable, and it grabs focus on pointer-down
// the way xterm's helper textarea does when you click the terminal.
function Terminal() {
	return (
		<div data-testid="terminal" onPointerDown={(event) => event.currentTarget.focus()} tabIndex={-1}>
			terminal
		</div>
	);
}

function Harness() {
	return (
		<>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<button type="button">Open menu</button>
				</DropdownMenuTrigger>
				<DropdownMenuContent>
					<DropdownMenuItem>Item</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
			<Terminal />
		</>
	);
}

// The shared DropdownMenu primitive bakes in PR #33's fix so EVERY dropdown in
// the app — not just the notifications bell — closes on a single outside click,
// lets that click land on (and focus) the terminal, and never leaves a stray
// focus ring on its trigger after a mouse dismiss. Keyboard closes still return
// focus to the trigger.
describe("DropdownMenuContent (shared overlay focus behavior)", () => {
	it("stays non-modal so an outside click reaches the page (no body pointer-events lock)", async () => {
		const user = userEvent.setup();
		render(<Harness />);

		await user.click(screen.getByRole("button", { name: "Open menu" }));
		expect(await screen.findByRole("menu")).toBeInTheDocument();

		// Modal menus set body { pointer-events: none }, swallowing the first click
		// on the terminal. A non-modal menu leaves outside clicks alone.
		expect(document.body.style.pointerEvents).not.toBe("none");
	});

	it("closes on a single outside click and lets that click focus the terminal, without stealing focus back to the trigger", async () => {
		const user = userEvent.setup();
		render(<Harness />);
		const trigger = screen.getByRole("button", { name: "Open menu" });

		await user.click(trigger);
		expect(await screen.findByRole("menu")).toBeInTheDocument();

		const terminal = screen.getByTestId("terminal");
		await user.click(terminal);

		await waitFor(() => expect(screen.queryByRole("menu")).not.toBeInTheDocument());
		expect(terminal).toHaveFocus();
		expect(trigger).not.toHaveFocus();
	});

	it("returns focus to the trigger when closed with Escape (keyboard accessibility)", async () => {
		const user = userEvent.setup();
		render(<Harness />);
		const trigger = screen.getByRole("button", { name: "Open menu" });

		await user.click(trigger);
		expect(await screen.findByRole("menu")).toBeInTheDocument();

		await user.keyboard("{Escape}");

		await waitFor(() => expect(screen.queryByRole("menu")).not.toBeInTheDocument());
		expect(trigger).toHaveFocus();
	});
});
