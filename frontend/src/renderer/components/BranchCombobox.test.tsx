import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { BranchCombobox } from "./BranchCombobox";

describe("BranchCombobox", () => {
	it("filters branches and selects one", async () => {
		const onChange = vi.fn();
		render(<BranchCombobox branches={["develop", "main", "origin/PROJ-2270"]} value="develop" onChange={onChange} />);
		await userEvent.click(screen.getByRole("textbox"));
		await userEvent.type(screen.getByRole("textbox"), "2270");
		await userEvent.click(screen.getByText("origin/PROJ-2270"));
		expect(onChange).toHaveBeenCalledWith("origin/PROJ-2270");
	});

	it("shows the full branch list on first open, before typing", async () => {
		const onChange = vi.fn();
		render(<BranchCombobox branches={["develop", "main", "origin/PROJ-2270"]} value="develop" onChange={onChange} />);
		await userEvent.click(screen.getByRole("textbox"));
		expect(screen.getByText("develop")).toBeInTheDocument();
		expect(screen.getByText("main")).toBeInTheDocument();
		expect(screen.getByText("origin/PROJ-2270")).toBeInTheDocument();
	});
});
