import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Switch } from "./switch";

describe("Switch", () => {
	it("fires onCheckedChange(true) when clicked from unchecked", async () => {
		const user = userEvent.setup();
		const onCheckedChange = vi.fn();
		render(<Switch checked={false} onCheckedChange={onCheckedChange} aria-label="toggle" />);

		await user.click(screen.getByRole("switch", { name: "toggle" }));

		expect(onCheckedChange).toHaveBeenCalledWith(true);
	});

	it("renders data-state=checked when checked", () => {
		render(<Switch checked onCheckedChange={() => {}} aria-label="toggle" />);

		expect(screen.getByRole("switch", { name: "toggle" })).toHaveAttribute("data-state", "checked");
	});

	it("renders data-state=unchecked when not checked", () => {
		render(<Switch checked={false} onCheckedChange={() => {}} aria-label="toggle" />);

		expect(screen.getByRole("switch", { name: "toggle" })).toHaveAttribute("data-state", "unchecked");
	});
});
