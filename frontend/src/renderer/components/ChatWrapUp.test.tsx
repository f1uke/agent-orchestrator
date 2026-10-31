import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ChatWrapUp } from "./ChatWrapUp";

const WRAP = {
	recap: "Tests pass; nothing is committed.",
	ask: "Commit these changes?",
	replies: ["Yes, commit", "Not yet"],
};

describe("ChatWrapUp", () => {
	it("shows the recap, the ask and a button per reply, the first one primary", () => {
		render(<ChatWrapUp live onPrompt={vi.fn()} wrap={WRAP} />);
		expect(screen.getByText(WRAP.recap)).toBeInTheDocument();
		expect(screen.getByText("Commit these changes?")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Yes, commit" }).className).toContain("primary");
		expect(screen.getByRole("button", { name: "Not yet" }).className).not.toContain("primary");
	});

	it("hands the label of a clicked reply to onPrompt, once", async () => {
		const onPrompt = vi.fn();
		render(<ChatWrapUp live onPrompt={onPrompt} wrap={WRAP} />);
		await userEvent.click(screen.getByRole("button", { name: "Not yet" }));
		expect(onPrompt).toHaveBeenCalledExactlyOnceWith("Not yet");
	});

	it("keeps an older box readable but its buttons dead", async () => {
		const onPrompt = vi.fn();
		render(<ChatWrapUp live={false} onPrompt={onPrompt} wrap={WRAP} />);
		const button = screen.getByRole("button", { name: "Yes, commit" });
		expect(button).toHaveAttribute("aria-disabled", "true");
		await userEvent.click(button);
		expect(onPrompt).not.toHaveBeenCalled();
	});

	it("shows an ask with no replies and a recap alone", () => {
		const { rerender } = render(
			<ChatWrapUp live onPrompt={vi.fn()} wrap={{ recap: "Done.", ask: "Next?", replies: [] }} />,
		);
		expect(screen.getByText("Next?")).toBeInTheDocument();
		expect(screen.queryByRole("button")).toBeNull();
		rerender(<ChatWrapUp live onPrompt={vi.fn()} wrap={{ recap: "Done.", replies: [] }} />);
		expect(screen.getByText("Done.")).toBeInTheDocument();
	});
});
