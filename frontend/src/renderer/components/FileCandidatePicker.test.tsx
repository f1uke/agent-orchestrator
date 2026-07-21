import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ResolvedCandidate } from "../lib/open-workspace-file";
import { FileCandidatePicker } from "./FileCandidatePicker";

const candidates: ResolvedCandidate[] = [
	{ path: "a/x.go", inWorkspace: true },
	{ path: "b/x.go", inWorkspace: true },
];

describe("FileCandidatePicker", () => {
	it("lists every candidate path", () => {
		render(<FileCandidatePicker open candidates={candidates} onPick={vi.fn()} onOpenChange={vi.fn()} />);
		expect(screen.getByText("a/x.go")).toBeInTheDocument();
		expect(screen.getByText("b/x.go")).toBeInTheDocument();
	});

	it("calls onPick with the chosen candidate, verdict included", async () => {
		const onPick = vi.fn();
		render(<FileCandidatePicker open candidates={candidates} onPick={onPick} onOpenChange={vi.fn()} />);
		await userEvent.click(screen.getByText("b/x.go"));
		expect(onPick).toHaveBeenCalledWith({ path: "b/x.go", inWorkspace: true });
	});

	// A picker row for a file outside the workspace must keep its verdict, so
	// picking it opens the standalone viewer rather than trying to reveal it.
	it("keeps an out-of-workspace verdict on the picked candidate", async () => {
		const onPick = vi.fn();
		const mixed: ResolvedCandidate[] = [
			{ path: "a/x.go", inWorkspace: true },
			{ path: "/elsewhere/x.go", inWorkspace: false },
		];
		render(<FileCandidatePicker open candidates={mixed} onPick={onPick} onOpenChange={vi.fn()} />);
		await userEvent.click(screen.getByText("/elsewhere/x.go"));
		expect(onPick).toHaveBeenCalledWith({ path: "/elsewhere/x.go", inWorkspace: false });
	});
});
