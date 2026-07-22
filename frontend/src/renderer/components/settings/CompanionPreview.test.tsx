import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ALL_COMPANION_STATUSES } from "../../../companion/scene";
import { CompanionPreview } from "./CompanionPreview";

describe("CompanionPreview", () => {
	it("stays folded away until asked for, so Settings is not a zoo by default", async () => {
		const { container } = render(<CompanionPreview />);

		expect(container.querySelectorAll("[data-preview-state]")).toHaveLength(0);

		await userEvent.click(screen.getByRole("button", { name: /what they look like/i }));

		expect(container.querySelectorAll("[data-preview-state]")).toHaveLength(ALL_COMPANION_STATUSES.length);
	});

	it("shows every state with a plain-English caption beside its id", async () => {
		render(<CompanionPreview />);
		await userEvent.click(screen.getByRole("button", { name: /what they look like/i }));

		expect(screen.getByText("Resting between jobs")).toBeInTheDocument();
		expect(screen.getByText("Waiting for you to answer")).toBeInTheDocument();
		expect(screen.getByText("idle")).toBeInTheDocument();
	});

	it("draws the Procs on a wallpaper range, not on a flat app panel", async () => {
		// Showing them on a panel would flatter the art and hide the exact failure the
		// ink rim exists to prevent: a body that vanishes against a mid-tone desktop.
		const { container } = render(<CompanionPreview />);
		await userEvent.click(screen.getByRole("button", { name: /what they look like/i }));

		const stage = container.querySelector("[data-preview-state]")?.closest("div[style]") as HTMLElement;
		expect(stage.style.background).toContain("gradient");
	});

	it("walks one claim through fresh, fading and settled", async () => {
		const { container } = render(<CompanionPreview />);
		await userEvent.click(screen.getByRole("button", { name: /what they look like/i }));

		expect(
			[...container.querySelectorAll("[data-preview-decay]")].map((n) => n.getAttribute("data-preview-decay")),
		).toEqual(["fresh", "fading", "settled"]);
	});

	it("folds back up again", async () => {
		const { container } = render(<CompanionPreview />);
		await userEvent.click(screen.getByRole("button", { name: /what they look like/i }));
		await userEvent.click(screen.getByRole("button", { name: /hide/i }));

		expect(container.querySelectorAll("[data-preview-state]")).toHaveLength(0);
	});
});
