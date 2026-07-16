import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { FileCandidatePicker } from "./FileCandidatePicker";

describe("FileCandidatePicker", () => {
	it("lists every candidate path", () => {
		render(<FileCandidatePicker open candidates={["a/x.go", "b/x.go"]} onPick={vi.fn()} onOpenChange={vi.fn()} />);
		expect(screen.getByText("a/x.go")).toBeInTheDocument();
		expect(screen.getByText("b/x.go")).toBeInTheDocument();
	});

	it("calls onPick with the chosen path", async () => {
		const onPick = vi.fn();
		render(<FileCandidatePicker open candidates={["a/x.go", "b/x.go"]} onPick={onPick} onOpenChange={vi.fn()} />);
		await userEvent.click(screen.getByText("b/x.go"));
		expect(onPick).toHaveBeenCalledWith("b/x.go");
	});
});
