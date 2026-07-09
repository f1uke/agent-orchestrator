import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { FileHeader } from "./FileHeader";

describe("FileHeader", () => {
	it("renders the basename prominently and the full path as the title, given a nested path", () => {
		render(<FileHeader path="a/b/c/File.swift" line={0} />);

		const basename = screen.getByText("File.swift");
		expect(basename).toBeInTheDocument();

		const header = basename.closest("[title]");
		expect(header).toHaveAttribute("title", "a/b/c/File.swift");
	});

	it("renders a path with no slash as-is", () => {
		render(<FileHeader path="a.go" line={0} />);

		const basename = screen.getByText("a.go");
		expect(basename).toBeInTheDocument();

		const header = basename.closest("[title]");
		expect(header).toHaveAttribute("title", "a.go");
	});
});
