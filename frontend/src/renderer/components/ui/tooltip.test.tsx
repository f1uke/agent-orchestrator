import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { SimpleTooltip, TooltipProvider } from "./tooltip";

// A tooltip is a passive hint. The shared primitive must (1) still appear on hover /
// focus, and (2) keep the popper non-interactive: pointer-events:none so moving the
// mouse onto the bubble can't hold it open, user-select:none so its copy can't be
// drag-selected. (The Radix "hoverable content" grace area — which is what actually
// kept a bubble stuck open under the pointer — is disabled at the Tooltip root via
// disableHoverableContent. That behavior is pointer-geometry driven, so it is verified
// in a real browser, not here in jsdom.)
describe("Tooltip primitive (passive, non-interactive hint)", () => {
	function renderTip() {
		return render(
			<TooltipProvider delayDuration={0}>
				<SimpleTooltip label="Group issues into sprint sections">
					<button type="button">Group by sprint</button>
				</SimpleTooltip>
			</TooltipProvider>,
		);
	}

	it("shows on focus and marks the popper non-interactive + non-selectable", async () => {
		const user = userEvent.setup();
		renderTip();

		await user.tab(); // focus the trigger → opens the tooltip
		await screen.findByRole("tooltip"); // a11y announcer span; wait for open

		// The visible styled bubble is the Radix popper content (carries data-side),
		// distinct from the visually-hidden role="tooltip" a11y span.
		const bubble = document.querySelector("[data-side]");
		expect(bubble).not.toBeNull();
		expect(bubble).toHaveTextContent("Group issues into sprint sections");

		// The fix: the bubble can neither capture the pointer nor have its text selected.
		expect(bubble).toHaveClass("pointer-events-none");
		expect(bubble).toHaveClass("select-none");
	});

	it("still appears on hover and hides on unhover (normal hint behavior preserved)", async () => {
		const user = userEvent.setup();
		renderTip();
		const trigger = screen.getByRole("button", { name: "Group by sprint" });

		await user.hover(trigger);
		expect(await screen.findByRole("tooltip")).toBeInTheDocument();

		await user.unhover(trigger);
		await waitFor(() => expect(screen.queryByRole("tooltip")).not.toBeInTheDocument());
	});
});
